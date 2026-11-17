// Package publication validates staged workbooks before owned atomic replacement.
package publication

import (
	"context"
	"errors"
	"os"

	"github.com/marcuoli/go-atomicfile/atomicfile"
	"github.com/marcuoli/go-xlsx-report/table"
	"github.com/marcuoli/go-xlsx-report/validation"
)

// Save writes a workbook to the caller-owned staging file without closing it.
type Save func(*os.File) error

// Publish returns whether installation happened, independently of cleanup errors.
// It never publishes a failed, cancelled, incomplete or value-altered workbook.
// The parent directory must exist. Evidence remains the caller's responsibility.
func Publish(ctx context.Context, path string, overwrite bool, save Save, expected []table.Sheet) (installed bool, returnedErr error) {
	return PublishSource(ctx, path, overwrite, save, table.FromSheets(expected))
}

// PublishSource validates one borrowed expected sheet at a time before atomic
// installation, using the same staging and cleanup contract as Publish. It visits
// expected once after save completes; the producer must replay the saved content
// deterministically. Late source errors, visitor failures and cancellation abort
// installation and remove the stage, preserving an existing destination. Actual
// package decoding retains ValidateSource's documented bounds and memory scope.
func PublishSource(ctx context.Context, path string, overwrite bool, save Save, expected table.Source) (installed bool, returnedErr error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if save == nil {
		return false, errors.New("workbook saver is required")
	}
	destination, err := atomicfile.NewPublication(path, overwrite)
	if err != nil {
		return false, err
	}
	staging, err := destination.CreateTemp(".xlsx-*")
	if err != nil {
		return false, err
	}
	closed := false
	defer func() {
		if !closed {
			returnedErr = errors.Join(returnedErr, staging.Close())
		}
		if err := os.Remove(staging.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
			returnedErr = errors.Join(returnedErr, err)
		}
	}()
	if err := save(staging); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := staging.Sync(); err != nil {
		return false, err
	}
	if err := staging.Close(); err != nil {
		closed = true
		return false, err
	}
	closed = true
	if err := validation.ValidateSource(ctx, staging.Name(), expected); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return destination.Commit(staging.Name())
}
