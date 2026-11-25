# Changelog

## v0.5.0

- Add replayable worksheet body rows with explicit per-row numeric values and adapters for existing sheet snapshots.
- Validate and atomically publish streamed worksheets one row at a time with bounded ZIP metadata and exact saved-value checks. Existing APIs retain their limits.

## v0.4.0

- Rename the module to github.com/marcuoli/go-document-xlsx. Update the module requirement and the path prefix in consumer imports. Package APIs and workbook behavior are unchanged.

## v0.3.0

- Add synchronous replayable worksheet sources and adapters, preserving borrowed-buffer ownership and propagating source, visitor and cancellation failures.
- Validate and atomically publish one expected sheet projection at a time through existing exact package checks and unchanged limits.
- Add deterministic Unicode-safe partition names with full-name hash suffixes for bounded long identities.
- Preserve legacy slice-publication saver/model/package error precedence and typed capacity errors.

## v0.2.0

- Add explicit precision-bounded numeric cell payloads and an optional backend contract, retaining literal text defaults.
- Validate saved numeric types and decimal equivalence, reject undeclared coercion and retain cancellation/error/publication checks.

## v0.1.0

- PR #2: add functional packages for literal cells/tables, Unicode layout, exact saved-package validation, streamed ZIP64 output and validated atomic publication.
- Preserve precise strings, formula-looking values, cancellation and failed-publication evidence. Include corruption, archive extent, escape-envelope and interoperability regression coverage.
