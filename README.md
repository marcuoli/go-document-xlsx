# go-document-xlsx

Reusable literal-table rendering, Unicode layout, saved XLSX validation, ZIP/ZIP64 output and atomic publication. Applications supply schemas, messages and concrete workbook adapters.

Use functional imports under `cell/`, `layout/`, `table/`, `validation/`, `publication/` and `zipio/`. The module has no dependency on a concrete workbook library and preserves exact numeric strings and formula-looking text as literal values.

```go
installed, err := publication.Publish(ctx, output, overwrite, saveWorkbook, sheets)
```

A saver writes to the caller-owned staging file without closing it. Publication validates every saved literal before installation. Inspect both `installed` and `err`; a cleanup error may follow installation. Create the output directory explicitly and keep the evidence snapshot when publication fails.

Use github.com/marcuoli/go-document-xlsx at v0.4.0 or later. When upgrading from v0.3.0 or earlier, update the module requirement and replace the github.com/marcuoli/go-xlsx-report prefix in imports; package names and APIs are unchanged.

Go 1.27.3 is required. Run `scripts/validate.ps1 -Race -Scan` or `scripts/validate.sh --race --scan` using approved external storage. The runnable `examples/archive` consumer takes an explicit output file and demonstrates the generic ZIP sink. Read [architecture](docs/architecture.md), [contribution/comment rules](CONTRIBUTING.md) and [code management](docs/code-management.md).

License: UPL 1.0.


## Explicit numeric cells

`table.Sheet.NumericCells` maps body coordinates such as `B2` to numeric payloads. The matching nonempty `Rows` text remains a display/layout hint. Headers and undeclared cells always remain literal text. A backend can opt into `cell.NumberWriter.SetCellNumber`; a numeric sheet fails before mutation when its backend lacks that optional interface. Existing text-only snapshots/backends retain their behavior.

`cell.Number` normalizes finite decimal/exponent literals only when they survive Excel's 15-significant-digit precision and normal binary64 range without decimal rounding. Larger exact counts and unsupported values should remain text. `cell.EqualNumber` admits equivalent decimal spellings in saved numeric payloads. Saved validation requires numeric type/value exactly where declared and continues rejecting formulas and undeclared numeric coercion. Styles/units and domain field meaning remain caller policy.

## Replayable sheet sources

Use `table.Source` when a workbook consists of bounded projections that should
not all remain in memory. Render each sheet with the existing `table.Render`,
then replay the same stable source through `publication.PublishSource` (or use
`validation.ValidateSource` to check an already saved package):

```go
source := table.Source(func(ctx context.Context, visit func(table.Sheet) error) error {
    for partition := 1; partition <= partitionCount; partition++ {
        sheet, err := loadPartition(ctx, partition)
        if err != nil {
            return err
        }
        if err := visit(sheet); err != nil {
            return err
        }
    }
    return ctx.Err()
})
if err := source.Visit(ctx, func(sheet table.Sheet) error {
    return table.Render(ctx, backend, sheet)
}); err != nil {
    return err
}
installed, err := publication.PublishSource(ctx, output, overwrite, saveWorkbook, source)
```

The source callbacks are synchronous and serial. A sheet's slices/maps are
borrowed only until its callback returns, so a producer can reuse buffers on its
next yield. Keep underlying inputs stable across rendering and validation;
publication rejects source errors and cancellation even after the final sheet.
`Source.Visit` latches rejected callbacks even when a producer ignores their
errors. Concurrent or delayed callbacks are unsupported. `table.FromSheets`
adapts an existing caller-owned slice without copying it; existing `Validate`
and `Publish` slice APIs remain available.

Validation consumes one expected sheet at a time. Actual worksheets and shared
strings are still decoded into memory within the unchanged 32 MiB compressed,
128 MiB expanded and 2,048-part package limits. These APIs do not add a new writer
or promise constant-memory workbook decoding.

`table.PartitionName(name, partition)` preserves a valid first name and prefixes
later names with `P2-`, `P3-`, and so on. Long names retain a readable Unicode
prefix and a stable short SHA-256 suffix of the full original identity. Callers
must check global case-insensitive name uniqueness, including other groups and
possible short-hash collisions; the helper does not reserve names. Saved
validation independently rejects duplicate identities.
