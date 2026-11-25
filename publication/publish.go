// Package publication validates staged workbooks before owned atomic replacement.
package publication

import (
	"context"
	"errors"
	"os"

	"github.com/marcuoli/go-atomicfile/atomicfile"
	"github.com/marcuoli/go-document-xlsx/table"
	"github.com/marcuoli/go-document-xlsx/validation"
)

// Save writes a workbook to the caller-owned staging file without closing it.
type Save func(*os.File) error

// Publish returns whether installation happened, independently of cleanup errors.
// It never publishes a failed, cancelled, incomplete or value-altered workbook.
// The parent directory must exist. Evidence remains the caller's responsibility.
func Publish(ctx context.Context, path string, overwrite bool, save Save, expected []table.Sheet) (installed bool, returnedErr error) {
	return publish(ctx, path, overwrite, save, func(stage string) error {
		return validation.Validate(ctx, stage, expected)
	})
}

// PublishSource validates one borrowed expected sheet at a time before atomic
// installation, using the same staging and cleanup contract as Publish. It visits
// expected once after save completes; the producer must replay the saved content
// deterministically. Late source errors, visitor failures and cancellation abort
// installation and remove the stage, preserving an existing destination. Actual
// package decoding retains ValidateSource's documented bounds and memory scope.
func PublishSource(ctx context.Context, path string, overwrite bool, save Save, expected table.Source) (installed bool, returnedErr error) {
	return publish(ctx, path, overwrite, save, func(stage string) error {
		return validation.ValidateSource(ctx, stage, expected)
	})
}

// PublishRows validates a workbook against a replayable, ordered row source.
// It uses the same staging and atomic installation as Publish. Validation visits
// the source once after saving, so producers must replay the saved rows exactly.
// A source error or cancellation preserves the destination and removes the stage.
func PublishRows(ctx context.Context, path string, overwrite bool, save Save, expected table.RowSheets) (bool, error) {
	return publish(ctx, path, overwrite, save, func(stage string) error {
		return validation.ValidateRows(ctx, stage, expected)
	})
}

// Each API selects its validator after the saver and stage close. This preserves
// legacy slice-model error precedence without preempting saver/staging failures.
func publish(ctx context.Context, path string, overwrite bool, save Save, validate func(string) error) (installed bool, returnedErr error) {
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
	if err := validate(staging.Name()); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return destination.Commit(staging.Name())
}
