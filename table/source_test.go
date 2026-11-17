package table_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/marcuoli/go-xlsx-report/table"
)

func TestSourceVisitRetainsRejectedPrefix(t *testing.T) {
	rejected := errors.New("rejected sheet")
	producerFailure := errors.New("producer failed after callbacks")
	calls := 0
	source := table.Source(func(_ context.Context, visitor func(table.Sheet) error) error {
		for range 3 {
			if err := visitor(table.Sheet{}); !errors.Is(err, rejected) {
				t.Fatalf("producer did not receive retained rejection: %v", err)
			}
		}
		return producerFailure
	})
	err := source.Visit(t.Context(), func(table.Sheet) error {
		calls++
		return rejected
	})
	if calls != 1 || !errors.Is(err, rejected) || !errors.Is(err, producerFailure) {
		t.Fatalf("calls=%d, error=%v", calls, err)
	}
}

func TestSourceVisitCancellation(t *testing.T) {
	for _, when := range []string{"before production", "before callback", "during callback", "after production"} {
		t.Run(when, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			produced, visited := 0, 0
			if when == "before production" {
				cancel()
			}
			source := table.Source(func(_ context.Context, visitor func(table.Sheet) error) error {
				produced++
				if when == "before callback" {
					cancel()
				}
				_ = visitor(table.Sheet{})
				_ = visitor(table.Sheet{})
				cancel()
				return nil
			})
			err := source.Visit(ctx, func(table.Sheet) error {
				visited++
				if when == "during callback" {
					cancel()
				}
				return nil
			})
			wantProduced, wantVisited := 1, 2
			switch when {
			case "before production":
				wantProduced, wantVisited = 0, 0
			case "before callback":
				wantVisited = 0
			case "during callback":
				wantVisited = 1
			}
			if !errors.Is(err, context.Canceled) || produced != wantProduced || visited != wantVisited {
				t.Fatalf("produced=%d, visited=%d, error=%v", produced, visited, err)
			}
		})
	}
}

func TestSourceVisitRequiresSourceAndVisitor(t *testing.T) {
	var source table.Source
	if err := source.Visit(t.Context(), func(table.Sheet) error { return nil }); err == nil {
		t.Fatal("nil source accepted")
	}
	if err := table.FromSheets(nil).Visit(t.Context(), nil); err == nil {
		t.Fatal("nil visitor accepted")
	}
}

func TestFromSheetsBorrowsAndReplaysInOrder(t *testing.T) {
	row := []string{"first"}
	sheets := []table.Sheet{{Name: "One", Rows: [][]string{row}}, {Name: "Two"}}
	source := table.FromSheets(sheets)
	for _, value := range []string{"first", "changed by owner"} {
		row[0] = value
		var names []string
		if err := source.Visit(t.Context(), func(sheet table.Sheet) error {
			names = append(names, sheet.Name)
			if sheet.Name == "One" && sheet.Rows[0][0] != value {
				t.Fatal("adapter cloned caller-owned data")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(names, []string{"One", "Two"}) {
			t.Fatalf("sheet order: %v", names)
		}
	}
	failure := errors.New("stop")
	calls := 0
	if err := source.Visit(t.Context(), func(table.Sheet) error { calls++; return failure }); !errors.Is(err, failure) || calls != 1 {
		t.Fatalf("adapter continued after failure: calls=%d error=%v", calls, err)
	}
}
