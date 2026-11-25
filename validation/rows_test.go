package validation_test

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcuoli/go-document-xlsx/internal/testbook"
	"github.com/marcuoli/go-document-xlsx/table"
	"github.com/marcuoli/go-document-xlsx/validation"
)

func replaySheets(sheets []table.Sheet) table.RowSheets {
	return func(_ context.Context, visit func(table.RowSheet) error) error {
		for _, sheet := range sheets {
			if err := visit(table.RowsFromSheet(sheet)); err != nil {
				return err
			}
		}
		return nil
	}
}

func TestValidateRowsAcceptsBorrowedLiteralAndNumericValues(t *testing.T) {
	first := sourceSheets()[0]
	first.Rows = append(first.Rows, []string{"'00", "9007199254740993", "4.25 units"})
	first.NumericCells["C3"] = "4.25"
	sheets := []table.Sheet{first, {Name: "Header only", Headers: []string{"Literal"}}}
	path := filepath.Join(t.TempDir(), "replay.xlsx")
	testbook.Write(t, path, testbook.Parts(sheets))
	source := table.RowSheets(func(_ context.Context, visit func(table.RowSheet) error) error {
		err := visit(table.RowSheet{Name: first.Name, Headers: first.Headers, Rows: func(_ context.Context, next func(table.Row) error) error {
			row := table.Row{Values: make([]string, 3), NumericCells: map[int]string{}}
			for index, values := range first.Rows {
				copy(row.Values, values)
				row.NumericCells[2] = first.NumericCells[fmt.Sprintf("C%d", index+2)]
				if err := next(row); err != nil {
					return err
				}
			}
			row.Values[0], row.NumericCells[2] = "reused", "0"
			return nil
		}})
		if err != nil {
			return err
		}
		return visit(table.RowSheet{Name: sheets[1].Name, Headers: sheets[1].Headers})
	})
	for range 2 {
		if err := validation.ValidateRows(t.Context(), path, source); err != nil {
			t.Fatal(err)
		}
	}
}

func TestValidateRowsRejectsAlteredSavedContent(t *testing.T) {
	const part = "xl/worksheets/sheet2.xml"
	const body = "<row r=\"2\">"
	for _, test := range []struct{ name, original, replacement, want string }{
		{"text", "9007199254740993", "9007199254740992", "value differs"},
		{"number", "<v>2.5</v>", "<v>2.50001</v>", "numeric type or value differs"},
		{"number type", "<c r=\"C2\" t=\"n\"><v>2.5</v></c>", "<c r=\"C2\" t=\"inlineStr\"><is><t>2.5</t></is></c>", "numeric type or value differs"},
		{"formula", "<v>2.5</v>", "<f>5/2</f><v>2.5</v>", "contains a formula"},
		{"row number", body, "<row r=\"3\">", "rows are reordered"},
		{"unexpected row", "</sheetData>", "<row r=\"3\"/></sheetData>", "row coverage differs"},
		{"foreign row namespace", body, "<row xmlns=\"urn:unrelated\" r=\"2\">", "element identity differs"},
		{"duplicate sheetData", "</worksheet>", "<sheetData/></worksheet>", "repeats sheetData"},
		{"trailing XML", "</worksheet>", "</worksheet><unexpected/>", "repeats its root"},
		{"oversized XML token", "9007199254740993", strings.Repeat("x", (4<<20)+1), "streaming capacity"},
	} {
		t.Run(test.name, func(t *testing.T) {
			sheets := sourceSheets()
			parts := testbook.Parts(sheets)
			parts[part] = strings.Replace(parts[part], test.original, test.replacement, 1)
			path := filepath.Join(t.TempDir(), "altered.xlsx")
			testbook.Write(t, path, parts)
			err := validation.ValidateRows(t.Context(), path, replaySheets(sheets))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("altered worksheet: got %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateRowsBeyondLegacyExpandedCapacity(t *testing.T) {
	const count = 8193
	const cellSize = 16384
	const part = "xl/worksheets/sheet1.xml"
	headers := []string{"Value"}
	sheet := table.Sheet{Name: "Large", Headers: headers}
	parts := testbook.Parts([]table.Sheet{sheet})
	found, err := os.Create(filepath.Join(t.TempDir(), "large.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	defer found.Close()
	writer := zip.NewWriter(found)
	for name, content := range parts {
		if name == part {
			continue
		}
		dest, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(dest, content); err != nil {
			t.Fatal(err)
		}
	}
	dest, err := writer.Create(part)
	if err != nil {
		t.Fatal(err)
	}
	start := strings.TrimSuffix(parts[part], "</sheetData></worksheet>")
	if _, err := io.WriteString(dest, start); err != nil {
		t.Fatal(err)
	}
	value := strings.Repeat("x", cellSize)
	for r := 2; r < count+2; r++ {
		if _, err := fmt.Fprintf(dest, "<row r=\"%d\"><c r=\"A%d\" t=\"inlineStr\"><is><t>%s</t></is></c></row>", r, r, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := io.WriteString(dest, "</sheetData></worksheet>"); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(writer.Close(), found.Close()); err != nil {
		t.Fatal(err)
	}
	if err := validation.Validate(t.Context(), found.Name(), []table.Sheet{sheet}); err == nil || !strings.Contains(err.Error(), "expanded XLSX exceeds capacity") {
		t.Fatalf("legacy byte limit changed: %v", err)
	}
	emitted := 0
	source := table.RowSheets(func(_ context.Context, visit func(table.RowSheet) error) error {
		return visit(table.RowSheet{Name: sheet.Name, Headers: headers, Rows: func(_ context.Context, next func(table.Row) error) error {
			row := table.Row{Values: []string{value}}
			for range count {
				if err := next(row); err != nil {
					return err
				}
				emitted++
			}
			return nil
		}})
	})
	if err := validation.ValidateRows(t.Context(), found.Name(), source); err != nil {
		t.Fatal(err)
	}
	if emitted != count {
		t.Fatalf("verified %d rows, want %d", emitted, count)
	}
}
