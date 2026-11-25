package publication_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/marcuoli/go-document-xlsx/internal/testbook"
	"github.com/marcuoli/go-document-xlsx/publication"
	"github.com/marcuoli/go-document-xlsx/table"
)

func TestPublishRowsInstallsOnlyFullyVerifiedReplay(t *testing.T) {
	const original = "previous installed bytes"
	failed := errors.New("row producer failed after final row")
	sheet := table.Sheet{Name: "Evidence", Headers: []string{"Name"}, Rows: [][]string{{"0007"}, {"=1+1"}}}
	for _, mode := range []string{"success", "late error", "late cancel", "ignored rejection", "missing row"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			dir := t.TempDir()
			path := filepath.Join(dir, "report.xlsx")
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			source := table.RowSheets(func(_ context.Context, visit func(table.RowSheet) error) error {
				return visit(table.RowSheet{Name: sheet.Name, Headers: sheet.Headers, Rows: func(_ context.Context, next func(table.Row) error) error {
					if mode == "ignored rejection" {
						_ = next(table.Row{Values: []string{"altered"}})
					}
					for i, values := range sheet.Rows {
						if i == 1 && mode == "missing row" {
							break
						}
						if err := next(table.Row{Values: values}); err != nil && mode != "ignored rejection" {
							return err
						}
					}
					switch mode {
					case "late error":
						return failed
					case "late cancel":
						cancel()
					}
					return nil
				}})
			})
			saves := 0
			installed, err := publication.PublishRows(ctx, path, true, func(f *os.File) error {
				saves++
				return testbook.WriteTo(f, testbook.Parts([]table.Sheet{sheet}))
			}, source)
			if mode == "success" {
				if !installed || err != nil {
					t.Fatalf("valid replay failed: %t, %v", installed, err)
				}
			} else {
				if installed || err == nil || mode == "late error" && !errors.Is(err, failed) || mode == "late cancel" && !errors.Is(err, context.Canceled) {
					t.Fatalf("failed replay installed or lost its error: %t, %v", installed, err)
				}
				content, err := os.ReadFile(path)
				if err != nil || string(content) != original {
					t.Fatalf("old destination changed: %q, %v", content, err)
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 || entries[0].Name() != "report.xlsx" || saves != 1 {
				t.Fatalf("unexpected saves or stages: %d, %v, %v", saves, entries, err)
			}
		})
	}
}
