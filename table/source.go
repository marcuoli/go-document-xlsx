package table

import (
	"context"
	"errors"
)

// Source visits sheets synchronously in serial order. Each sheet and its slices
// and maps are borrowed until the visitor returns; neither Source nor Visit
// retains copies. Producers must stop on visitor errors and observe ctx. They
// must provide stable, deterministic replay when reused for rendering and saved
// validation, even if they reuse buffers between callbacks. Asynchronous or
// concurrent callbacks, including callbacks after Source returns, are unsupported.
// Call Visit to retain visitor and cancellation errors from a misbehaving producer.
type Source func(context.Context, func(Sheet) error) error

// Visit invokes the source without retaining sheets. It rejects nil sources and
// visitors, observes cancellation before and after production and each callback,
// and preserves the first callback failure even if the producer ignores it.
// After a callback fails, no later callback reaches the visitor. Producer errors
// and cancellation remain discoverable through errors.Is.
func (source Source) Visit(ctx context.Context, visitor func(Sheet) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if source == nil || visitor == nil {
		return errors.New("worksheet source and visitor are required")
	}
	var rejected error
	produced := source(ctx, func(sheet Sheet) error {
		if rejected != nil {
			return rejected
		}
		if err := ctx.Err(); err != nil {
			rejected = err
			return rejected
		}
		rejected = errors.Join(visitor(sheet), ctx.Err())
		return rejected
	})
	return errors.Join(rejected, produced, ctx.Err())
}

// FromSheets adapts caller-owned snapshots without cloning slices, rows or maps.
// The caller must keep them unchanged throughout each visit and any replay.
// An empty or nil slice produces no sheets; consumers decide whether that is valid.
func FromSheets(sheets []Sheet) Source {
	return func(ctx context.Context, visitor func(Sheet) error) error {
		for _, sheet := range sheets {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := errors.Join(visitor(sheet), ctx.Err()); err != nil {
				return err
			}
		}
		return ctx.Err()
	}
}
