package validation

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"strings"

	"github.com/marcuoli/go-document-xlsx/table"
)

const (
	maxXMLTokenSize    = 4 << 20
	maxWorksheetRowXML = 16 << 20
	maxXMLDepth        = 256
)

// ValidateRows verifies ZIP integrity, sheet order and exact literal cells while
// visiting one expected and one saved row at a time. Worksheet XML can exceed
// ValidateSource's expanded size bound. Generated packages have at most 2,048 parts, a
// 4 MiB directory and unprefixed 32-bit offsets, without a ZIP comment. Other parts together have a 128 MiB
// expanded limit. XML tokens, depth and saved row bytes are also bounded.
func ValidateRows(ctx context.Context, path string, expected table.RowSheets) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if expected == nil {
		return errors.New("expected worksheet row source is required")
	}
	book, err := openWorkbook(ctx, path, true)
	if err != nil {
		return err
	}
	defer book.file.Close()
	seen := map[string]bool{}
	index := 0
	if err := expected.Visit(ctx, func(sheet table.RowSheet) error {
		entry, err := book.sheet(index, seen, sheet.Name)
		if err != nil {
			return err
		}
		if err := validateSheetRows(ctx, entry, sheet, book.strings); err != nil {
			return err
		}
		index++
		return nil
	}); err != nil {
		return err
	}
	return book.checkCount(index)
}

func validateSheetRows(ctx context.Context, entry *zip.File, sheet table.RowSheet, stringsTable []string) (err error) {
	stream, err := entry.Open()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, stream.Close()) }()
	window := newXMLWindow(stream)
	rows := worksheetRows{decoder: xml.NewDecoder(window), window: window}

	check := func(index int, want table.Row) error {
		row, ok, err := rows.next(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("saved worksheet row coverage differs")
		}
		numbers := make(map[string]string, len(want.NumericCells))
		for column, value := range want.NumericCells {
			axis, _ := table.Axis(column+1, index)
			numbers[axis] = value
		}
		return compareSavedRow(sheet.Name, index, row, want.Values, numbers, stringsTable)
	}
	if err := table.Validate(table.Sheet{Name: sheet.Name, Headers: sheet.Headers}); err != nil {
		return err
	}
	if err := check(1, table.Row{Values: sheet.Headers}); err != nil {
		return err
	}
	if err := sheet.Visit(ctx, check); err != nil {
		return err
	}
	_, extra, err := rows.next(ctx)
	if err != nil {
		return err
	}
	if extra {
		return errors.New("saved worksheet row coverage differs")
	}
	return nil
}

// worksheetRows follows the worksheet and sheetData elements, decoding only
// their individual row children. openWorkbook already verified document roots,
// token bounds and namespaces required by the package-level contract.
type worksheetRows struct {
	decoder  *xml.Decoder
	window   *xmlWindow
	depth    int
	seenData bool
	inData   bool
}

func (rows *worksheetRows) next(ctx context.Context) (savedRow, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return savedRow{}, false, err
		}
		rows.window.remaining = maxXMLTokenSize
		start := rows.decoder.InputOffset()
		token, err := rows.decoder.Token()
		if err == io.EOF && rows.depth == 0 && rows.seenData {
			return savedRow{}, false, nil
		}
		if err != nil {
			return savedRow{}, false, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			if rows.inData && rows.depth == 2 {
				rows.window.remaining = maxWorksheetRowXML - int(rows.decoder.InputOffset()-start)
				var row savedRow
				if err := rows.decoder.DecodeElement(&row, &token); err != nil {
					return savedRow{}, false, err
				}
				return row, true, nil
			}
			if rows.depth == 1 {
				if token.Name.Space != mainNamespace {
					return savedRow{}, false, errors.New("worksheet child namespace differs")
				}
				if token.Name.Local == "sheetData" {
					if rows.seenData {
						return savedRow{}, false, errors.New("worksheet repeats sheetData")
					}
					rows.seenData, rows.inData = true, true
				}
			}
			rows.depth++
		case xml.EndElement:
			rows.depth--
			if rows.depth == 1 {
				rows.inData = false
			}
		case xml.CharData:
			if (rows.depth == 1 || rows.inData) && strings.TrimSpace(string(token)) != "" {
				return savedRow{}, false, errors.New("worksheet contains untagged content")
			}
		}
	}
}

var errXMLCapacity = errors.New("XLSX XML token or row exceeds streaming capacity")

// The decoder reads through io.ByteReader, so resetting the allowance never
// counts bytes prefetched for the next token or worksheet row.
type xmlWindow struct {
	stream    *bufio.Reader
	remaining int
}

func newXMLWindow(stream io.Reader) *xmlWindow {
	return &xmlWindow{stream: bufio.NewReader(stream)}
}

func (window *xmlWindow) ReadByte() (byte, error) {
	if window.remaining <= 0 {
		return 0, errXMLCapacity
	}
	window.remaining--
	return window.stream.ReadByte()
}

func (window *xmlWindow) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if window.remaining <= 0 {
		return 0, errXMLCapacity
	}
	n, err := window.stream.Read(p[:min(len(p), window.remaining)])
	window.remaining -= n
	return n, err
}
