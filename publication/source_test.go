package publication_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcuoli/go-document-xlsx/internal/testbook"
	"github.com/marcuoli/go-document-xlsx/publication"
	"github.com/marcuoli/go-document-xlsx/table"
	"github.com/marcuoli/go-document-xlsx/validation"
)

func TestPublishSourcePreservesDestinationAndCleansStage(t *testing.T) {
	sheets := []table.Sheet{{Name: "First", Headers: []string{"Value"}, Rows: [][]string{{"00123"}}}, {Name: "Second", Headers: []string{"Value"}, Rows: [][]string{{"=1+1"}}}}
	failure := errors.New("late source failure")
	for _, outcome := range []string{"success", "late failure", "late cancel", "ignored rejection", "corrupt workbook"} {
		t.Run(outcome, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "report.xlsx")
			original := []byte("previous installed bytes")
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			parts := testbook.Parts(sheets)
			if outcome == "corrupt workbook" {
				parts["xl/worksheets/sheet2.xml"] = strings.Replace(parts["xl/worksheets/sheet2.xml"], "=1+1", "=1+2", 1)
			}
			source := table.Source(func(ctx context.Context, visitor func(table.Sheet) error) error {
				if outcome == "ignored rejection" {
					_ = visitor(table.Sheet{Name: "Rejected"})
				}
				if err := table.FromSheets(sheets).Visit(ctx, visitor); err != nil && outcome != "ignored rejection" {
					return err
				}
				if outcome == "late failure" {
					return failure
				}
				if outcome == "late cancel" {
					cancel()
				}
				return nil
			})
			installed, err := publication.PublishSource(ctx, path, true, func(file *os.File) error { return testbook.WriteTo(file, parts) }, source)
			if outcome == "success" {
				if !installed || err != nil {
					t.Fatalf("valid source failed publication: %v, %v", installed, err)
				}
				if err := validation.ValidateSource(ctx, path, table.FromSheets(sheets)); err != nil {
					t.Fatalf("installed content differs: %v", err)
				}
			} else {
				if installed || err == nil || outcome == "late failure" && !errors.Is(err, failure) || outcome == "late cancel" && !errors.Is(err, context.Canceled) {
					t.Fatalf("failed source installed or error lost: %v, %v", installed, err)
				}
				actual, readErr := os.ReadFile(path)
				if readErr != nil || !bytes.Equal(actual, original) {
					t.Fatalf("previous destination changed: %q, %v", actual, readErr)
				}
			}
			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != 1 || entries[0].Name() != "report.xlsx" {
				t.Fatalf("staging leftovers: %v, %v", entries, err)
			}
		})
	}
}
