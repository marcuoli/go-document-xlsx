package validation

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"strconv"
	"strings"

	"github.com/marcuoli/go-document-xlsx/cell"
	"github.com/marcuoli/go-document-xlsx/table"
)

type textItem = stringItem

// Validate checks ZIP integrity, part identities, sheet order, exact row coverage
// and every saved literal value/type, including declared numeric cells. It rejects
// formulas and external relationships; undeclared cells retain exact text semantics.
// Fixed file/expanded/entry limits bound verification before atomic publication.
func Validate(ctx context.Context, path string, expected []table.Sheet) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Preserve the slice API's early model validation before touching the file.
	for _, sheet := range expected {
		if err := table.Validate(sheet); err != nil {
			return err
		}
	}
	return ValidateSource(ctx, path, table.FromSheets(expected))
}

// ValidateSource checks the same exact package contract as Validate while
// consuming one borrowed expected sheet at a time without retaining its model.
// It visits expected once, in workbook order; empty, missing, extra, reordered or
// case-duplicate sheets and any producer error or cancellation invalidate the
// result. Actual worksheet and shared-string decoding remains materialized within
// the existing 32 MiB compressed, 128 MiB expanded and 2,048 ZIP-part limits.
func ValidateSource(ctx context.Context, path string, expected table.Source) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if expected == nil {
		return errors.New("expected worksheet source is required")
	}
	book, err := openWorkbook(ctx, path, false)
	if err != nil {
		return err
	}
	defer book.file.Close()
	seen := map[string]bool{}
	index := 0
	if err := expected.Visit(ctx, func(sheet table.Sheet) error {
		if err := table.Validate(sheet); err != nil {
			return err
		}
		entry, err := book.sheet(index, seen, sheet.Name)
		if err != nil {
			return err
		}
		var worksheet worksheetDocument
		if err := decodeArchiveEntry(entry, &worksheet); err != nil {
			return err
		}
		rows := [][]string{sheet.Headers}
		rows = append(rows, sheet.Rows...)
		if len(worksheet.Rows) != len(rows) {
			return errors.New("saved worksheet row coverage differs")
		}
		for r, row := range worksheet.Rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := compareSavedRow(sheet.Name, r+1, row, rows[r], sheet.NumericCells, book.strings); err != nil {
				return err
			}
		}
		index++
		return nil
	}); err != nil {
		return err
	}
	return book.checkCount(index)
}

type savedWorkbook struct {
	file    *os.File
	entries map[string]*zip.File
	parts   map[string]string
	types   map[string]string
	names   []string
	strings []string
}

const maxExpandedWorkbook = 128 << 20

// A generated ZIP whose local headers fit ordinary 32-bit offsets can have
// ZIP64 worksheet member sizes. Directory ZIP64 is outside this contract.
const maxStreamWorkbookSize = (1 << 32) - 1

func openWorkbook(ctx context.Context, path string, streaming bool) (_ *savedWorkbook, returnedErr error) {
	archiveSize := int64(maxWorkbookSize)
	if streaming {
		archiveSize = maxStreamWorkbookSize
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > archiveSize {
		return nil, errors.New("XLSX is not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if returnedErr != nil {
			returnedErr = errors.Join(returnedErr, file.Close())
		}
	}()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(info, actual) || info.Size() != actual.Size() {
		return nil, errors.New("XLSX file identity changed during verification")
	}
	directory, length, base, members, err := directoryExtent(file, info.Size(), streaming)
	if err != nil {
		return nil, err
	}
	if streaming {
		if err := checkDirectorySize(file, directory, length, base, members); err != nil {
			return nil, err
		}
	}
	archive, err := zip.NewReader(file, info.Size())
	if err != nil {
		return nil, err
	}
	if len(archive.File) > 2048 {
		return nil, errors.New("XLSX has too many parts")
	}
	if err := compressedBounds(file, directory, length, base, archive.File); err != nil {
		return nil, err
	}
	entries := make(map[string]*zip.File, len(archive.File))
	var worksheets []*zip.File
	var expanded uint64
	for _, entry := range archive.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name
		if name == "" || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || pathpkg.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || entries[name] != nil {
			return nil, errors.New("invalid or duplicated XLSX part")
		}
		entries[name] = entry
		if streaming && strings.HasPrefix(name, "xl/worksheets/") && strings.HasSuffix(name, ".xml") {
			worksheets = append(worksheets, entry)
			continue
		}
		if entry.UncompressedSize64 > maxExpandedWorkbook || expanded > maxExpandedWorkbook-entry.UncompressedSize64 {
			return nil, errors.New("expanded XLSX exceeds capacity")
		}
		expanded += entry.UncompressedSize64
		xmlPart := strings.HasSuffix(strings.ToLower(name), ".xml") || strings.HasSuffix(strings.ToLower(name), ".rels")
		if streaming && xmlPart {
			// Parsing through EOF also checks the archive reader's size and
			// CRC. No separate decompression pass is needed for these parts.
			if err := validateXMLPart(ctx, entry, true); err != nil {
				return nil, err
			}
		} else {
			stream, err := entry.Open()
			if err != nil {
				return nil, err
			}
			readBytes, readErr := io.Copy(io.Discard, io.LimitReader(stream, maxExpandedWorkbook+1))
			closeErr := stream.Close()
			if readBytes != int64(entry.UncompressedSize64) || readBytes > maxExpandedWorkbook {
				return nil, errors.New("XLSX expanded size differs from its declared capacity")
			}
			if err := errors.Join(readErr, closeErr); err != nil {
				return nil, err
			}
			if xmlPart {
				if err := validateXMLPart(ctx, entry, false); err != nil {
					return nil, err
				}
			}
		}
		if strings.HasSuffix(name, ".rels") {
			var rels struct {
				Items []struct {
					TargetMode string `xml:"TargetMode,attr"`
				} `xml:"Relationship"`
			}
			if err := decodeArchiveEntry(entry, &rels); err != nil {
				return nil, err
			}
			for _, rel := range rels.Items {
				if strings.EqualFold(rel.TargetMode, "External") {
					return nil, errors.New("external XLSX relationships are prohibited")
				}
			}
		}
	}
	var contentTypes struct {
		Overrides []struct {
			PartName    string `xml:"PartName,attr"`
			ContentType string `xml:"ContentType,attr"`
		} `xml:"Override"`
	}
	if err := decodeRequiredArchiveEntry(entries, "[Content_Types].xml", &contentTypes); err != nil {
		return nil, err
	}
	workbookType := false
	partTypes := map[string]string{}
	for _, part := range contentTypes.Overrides {
		if partTypes[part.PartName] != "" {
			return nil, errors.New("XLSX content type is duplicated")
		}
		partTypes[part.PartName] = part.ContentType
		if part.PartName == "/xl/workbook.xml" && part.ContentType == "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml" {
			workbookType = true
		}
		if strings.Contains(strings.ToLower(part.ContentType), "macroenabled") || strings.Contains(strings.ToLower(part.ContentType), "vbaproject") {
			return nil, errors.New("macro-enabled packages are unsupported")
		}
	}
	if !workbookType {
		return nil, errors.New("workbook content type is missing")
	}
	var rootRelationships struct {
		Items []struct {
			Type   string `xml:"Type,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if err := decodeRequiredArchiveEntry(entries, "_rels/.rels", &rootRelationships); err != nil {
		return nil, err
	}
	rootFound := false
	rootCount := 0
	for _, relationship := range rootRelationships.Items {
		if relationship.Type == "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" {
			rootCount++
		}
		if relationship.Type == "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" && relationship.Target == "xl/workbook.xml" {
			rootFound = true
		}
	}
	if !rootFound || rootCount != 1 {
		return nil, errors.New("workbook root relationship is missing")
	}
	parts, err := worksheetParts(entries)
	if err != nil {
		return nil, err
	}
	var book struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := decodeRequiredArchiveEntry(entries, "xl/workbook.xml", &book); err != nil {
		return nil, err
	}
	stringsTable := []string{}
	if shared := entries["xl/sharedStrings.xml"]; shared != nil {
		var values struct {
			Items []textItem `xml:"si"`
		}
		if err := decodeArchiveEntry(shared, &values); err != nil {
			return nil, err
		}
		for _, item := range values.Items {
			value, err := item.value()
			if err != nil {
				return nil, err
			}
			stringsTable = append(stringsTable, value)
		}
	}
	if streaming {
		referenced := make(map[string]bool, len(parts))
		for _, part := range parts {
			referenced[part] = true
		}
		for _, worksheet := range worksheets {
			if !referenced[worksheet.Name] {
				return nil, errors.New("XLSX contains an unreferenced worksheet part")
			}
			// Parsing through EOF verifies member size and CRC. This pass
			// bounds the structure and XML tokens before semantic row replay.
			if err := validateXMLPart(ctx, worksheet, true); err != nil {
				return nil, err
			}
		}
	}
	names := make([]string, 0, len(book.Sheets))
	for _, sheet := range book.Sheets {
		names = append(names, sheet.Name)
	}
	return &savedWorkbook{file: file, entries: entries, parts: parts, types: partTypes, names: names, strings: stringsTable}, nil
}

func (book *savedWorkbook) sheet(index int, seen map[string]bool, name string) (*zip.File, error) {
	if index >= len(book.names) || index >= len(book.parts) {
		return nil, errors.New("saved worksheet set differs")
	}
	key := strings.ToLower(name)
	if seen[key] || book.names[index] != name {
		return nil, errors.New("saved worksheet order/identity differs")
	}
	seen[key] = true
	part, ok := book.parts[name]
	if !ok {
		return nil, errors.New("saved worksheet is missing")
	}
	if book.types["/"+part] != "application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml" {
		return nil, errors.New("worksheet content type is missing or changed")
	}
	entry := book.entries[part]
	if entry == nil {
		return nil, fmt.Errorf("workbook package entry %q is missing", part)
	}
	return entry, nil
}

func (book *savedWorkbook) checkCount(index int) error {
	if index == 0 {
		return errors.New("expected worksheet set is empty")
	}
	if index != len(book.parts) || index != len(book.names) {
		return errors.New("saved worksheet set differs")
	}
	return nil
}

func compareSavedRow(sheetName string, index int, row savedRow, values []string, numbers map[string]string, stringsTable []string) error {
	if row.Index != index {
		return errors.New("saved worksheet rows are reordered")
	}
	actual := map[string]string{}
	for _, saved := range row.Cells {
		if saved.Formula != nil {
			return errors.New("saved literal cell contains a formula")
		}
		if _, duplicate := actual[saved.Axis]; duplicate {
			return errors.New("saved cell is duplicated")
		}
		if number, declared := numbers[saved.Axis]; declared {
			if saved.Kind != "" && saved.Kind != "n" || !saved.hasValue || saved.hasInline || !cell.EqualNumber(saved.Value, number) {
				return errors.New("saved numeric type or value differs")
			}
			actual[saved.Axis] = number
			continue
		}
		value := ""
		switch saved.Kind {
		case "s":
			if !saved.hasValue || saved.hasInline {
				return errors.New("shared string value child differs")
			}
			n, err := strconv.Atoi(saved.Value)
			if err != nil || n < 0 || n >= len(stringsTable) {
				return errors.New("invalid shared string reference")
			}
			value = stringsTable[n]
		case "inlineStr":
			if !saved.hasInline || saved.hasValue {
				return errors.New("inline string value child differs")
			}
			var err error
			value, err = saved.Inline.value()
			if err != nil {
				return err
			}
		case "":
			if saved.Value != "" || saved.hasValue || saved.hasInline {
				return errors.New("literal text changed to a numeric cell")
			}
		default:
			return errors.New("saved literal cell type differs")
		}
		actual[saved.Axis] = value
	}
	for c, want := range values {
		axis, _ := table.Axis(c+1, index)
		got, exists := actual[axis]
		if number, declared := numbers[axis]; declared {
			want = number
		}
		if got != want || !exists && want != "" {
			return fmt.Errorf("saved literal value differs at %s!%s", sheetName, axis)
		}
		delete(actual, axis)
	}
	if len(actual) != 0 {
		return errors.New("saved worksheet contains unexpected cells")
	}
	return nil
}
