package table

import (
	"context"
	"errors"
	"fmt"

	"github.com/marcuoli/go-document-xlsx/cell"
)

// Row holds one body row. NumericCells indexes explicitly numeric values by
// zero-based column. Values remains the literal text or numeric display hint.
type Row struct {
	Values       []string
	NumericCells map[int]string
}

// RowSource visits body rows synchronously in worksheet order. Row data is
// borrowed until the visitor returns. Producers must stop on visitor errors and
// observe context cancellation. Replay for rendering and validation must yield
// the same content even when row buffers are reused.
type RowSource func(context.Context, func(Row) error) error

// RowSheet describes one worksheet without retaining its data rows. Headers
// and any borrowed metadata remain valid for the duration of the sheet visit.
// A nil Rows source describes a header-only worksheet.
type RowSheet struct {
	Name    string
	Headers []string
	Rows    RowSource
}

// Visit checks headers and body rows, and passes each valid row and its one-based
// worksheet index to visitor. Index 1 belongs to the header. The method never
// retains rows and preserves the first visitor failure and cancellation.
func (sheet RowSheet) Visit(ctx context.Context, visitor func(worksheetRow int, row Row) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if visitor == nil {
		return errors.New("row visitor is required")
	}
	if err := Validate(Sheet{Name: sheet.Name, Headers: sheet.Headers}); err != nil {
		return err
	}
	if sheet.Rows == nil {
		return ctx.Err()
	}
	index := 1
	return visitOrdered(ctx, sheet.Rows, func(row Row) error {
		index++
		if index > 1048576 {
			return errors.New("invalid worksheet dimensions")
		}
		if len(row.Values) != len(sheet.Headers) {
			return errors.New("row width differs from header")
		}
		for column, number := range row.NumericCells {
			if column < 0 || column >= len(row.Values) || row.Values[column] == "" {
				return errors.New("numeric declaration must identify a nonempty body cell")
			}
			if _, ok := cell.Number(number); !ok {
				return fmt.Errorf("%w: numeric declaration", cell.ErrInvalidNumber)
			}
		}
		for column, value := range row.Values {
			axis, _ := Axis(column+1, index)
			if err := cell.CheckText(sheet.Name, axis, value); err != nil {
				return err
			}
		}
		return visitor(index, row)
	})
}

// RowSheets visits worksheets synchronously in workbook order. Producers must
// keep each sheet and its metadata valid during the callback and provide stable
// content when reused across multiple passes.
type RowSheets func(context.Context, func(RowSheet) error) error

// Visit preserves visitor errors and cancellation without retaining sheets.
func (source RowSheets) Visit(ctx context.Context, visitor func(RowSheet) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if source == nil || visitor == nil {
		return errors.New("worksheet row source and visitor are required")
	}
	return visitOrdered(ctx, source, visitor)
}

// RowsFromSheet adapts an existing immutable sheet without cloning its body.
// Full snapshot validation runs before yielding rows so numeric coordinate
// declarations outside the body cannot disappear during adaptation.
func RowsFromSheet(sheet Sheet) RowSheet {
	return RowSheet{
		Name: sheet.Name, Headers: sheet.Headers,
		Rows: func(ctx context.Context, visit func(Row) error) error {
			if err := Validate(sheet); err != nil {
				return err
			}
			for index, values := range sheet.Rows {
				row := Row{Values: values}
				if len(sheet.NumericCells) != 0 {
					for column := range values {
						axis, _ := Axis(column+1, index+2)
						if value, ok := sheet.NumericCells[axis]; ok {
							if row.NumericCells == nil {
								row.NumericCells = make(map[int]string)
							}
							row.NumericCells[column] = value
						}
					}
				}
				if err := visit(row); err != nil {
					return err
				}
			}
			return ctx.Err()
		},
	}
}

func visitOrdered[T any](ctx context.Context, source func(context.Context, func(T) error) error, visit func(T) error) error {
	var rejected error
	produced := source(ctx, func(item T) error {
		if rejected != nil {
			return rejected
		}
		if err := ctx.Err(); err != nil {
			rejected = err
			return err
		}
		rejected = errors.Join(visit(item), ctx.Err())
		return rejected
	})
	return errors.Join(rejected, produced, ctx.Err())
}
