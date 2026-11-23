package validation_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcuoli/go-document-xlsx/internal/testbook"
	"github.com/marcuoli/go-document-xlsx/table"
	"github.com/marcuoli/go-document-xlsx/validation"
)

func sourceSheets() []table.Sheet {
	return []table.Sheet{
		{Name: "First", Headers: []string{"Name", "Value", "Count"}, Rows: [][]string{{"=1+1", "ação 東京 😀", "1.25 units"}}, NumericCells: map[string]string{"C2": "1.25"}},
		{Name: "Second", Headers: []string{"Name", "Value", "Count"}, Rows: [][]string{{"00123", "9007199254740993", "2.5 units"}}, NumericCells: map[string]string{"C2": "2.5"}},
	}
}

func TestValidateSourceConsumesBorrowedModelsBeforeNextYield(t *testing.T) {
	sheets := sourceSheets()
	path := filepath.Join(t.TempDir(), "partitions.xlsx")
	testbook.Write(t, path, testbook.Parts(sheets))
	source := table.Source(func(ctx context.Context, visitor func(table.Sheet) error) error {
		row := make([]string, 3)
		numbers := map[string]string{}
		for _, sheet := range sheets {
			copy(row, sheet.Rows[0])
			numbers["C2"] = sheet.NumericCells["C2"]
			sheet.Rows, sheet.NumericCells = [][]string{row}, numbers
			if err := visitor(sheet); err != nil {
				return err
			}
		}
		// A successful visitor must already have consumed this borrowed data.
		row[0], numbers["C2"] = "buffer reused", "999"
		return ctx.Err()
	})
	for range 2 {
		if err := validation.ValidateSource(t.Context(), path, source); err != nil {
			t.Fatal(err)
		}
	}
}

func TestValidateSourceRejectsIncompleteOrRejectedExpectations(t *testing.T) {
	sheets := sourceSheets()
	path := filepath.Join(t.TempDir(), "partitions.xlsx")
	testbook.Write(t, path, testbook.Parts(sheets))
	failure := errors.New("late source failure")
	for _, name := range []string{"nil", "empty", "fewer", "more", "reordered", "duplicate", "invalid model", "late failure", "late cancel", "ignored rejection"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var source table.Source
			switch name {
			case "empty":
				source = table.FromSheets(nil)
			case "fewer":
				source = table.FromSheets(sheets[:1])
			case "more":
				source = table.FromSheets(append(sourceSheets(), sheets[0]))
			case "reordered":
				source = table.FromSheets([]table.Sheet{sheets[1], sheets[0]})
			case "duplicate":
				source = table.FromSheets([]table.Sheet{sheets[0], sheets[0]})
			case "invalid model":
				source = table.FromSheets([]table.Sheet{{Name: "Invalid"}})
			case "late failure", "late cancel":
				source = func(ctx context.Context, visitor func(table.Sheet) error) error {
					if err := table.FromSheets(sheets).Visit(ctx, visitor); err != nil {
						return err
					}
					if name == "late cancel" {
						cancel()
						return nil
					}
					return failure
				}
			case "ignored rejection":
				source = func(_ context.Context, visitor func(table.Sheet) error) error {
					wrong := sheets[0]
					wrong.Rows = [][]string{{"changed", "value", "1.25 units"}}
					_ = visitor(wrong)
					for _, sheet := range sheets {
						_ = visitor(sheet)
					}
					return nil
				}
			}
			err := validation.ValidateSource(ctx, path, source)
			if err == nil || name == "late failure" && !errors.Is(err, failure) || name == "late cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("%s expectation accepted or error lost: %v", name, err)
			}
		})
	}
}

func TestValidateSourceRejectsCaseDuplicateWorkbook(t *testing.T) {
	sheets := sourceSheets()
	sheets[1].Name = "first"
	path := filepath.Join(t.TempDir(), "duplicate.xlsx")
	testbook.Write(t, path, testbook.Parts(sheets))
	if err := validation.ValidateSource(t.Context(), path, table.FromSheets(sheets)); err == nil {
		t.Fatal("case-insensitive duplicate sheet names accepted")
	}
}

func TestValidateSourceRetainsExactSecondSheetChecks(t *testing.T) {
	sheets := sourceSheets()
	for _, test := range []struct{ name, old, replacement string }{
		{"text changed", "9007199254740993", "9007199254740992"},
		{"numeric value changed", "<v>2.5</v>", "<v>2.500000000000001</v>"},
		{"numeric became text", `<c r="C2" t="n"><v>2.5</v></c>`, `<c r="C2" t="inlineStr"><is><t>2.5</t></is></c>`},
		{"formula", "<v>2.5</v>", "<f>5/2</f><v>2.5</v>"},
		{"row coverage", `<row r="2">`, `<row r="3">`},
	} {
		t.Run(test.name, func(t *testing.T) {
			parts := testbook.Parts(sheets)
			parts["xl/worksheets/sheet2.xml"] = strings.Replace(parts["xl/worksheets/sheet2.xml"], test.old, test.replacement, 1)
			path := filepath.Join(t.TempDir(), "corrupt.xlsx")
			testbook.Write(t, path, parts)
			if err := validation.ValidateSource(t.Context(), path, table.FromSheets(sheets)); err == nil {
				t.Fatal("second-sheet corruption accepted")
			}
		})
	}
}
