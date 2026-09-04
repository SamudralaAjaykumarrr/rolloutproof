# ADR 0001: Normalized IR over direct rule checks on raw artifacts

## Status

Accepted

## Context

An invariant like "destructive column removal while old readers remain"
needs facts from at least three different formats: Kubernetes YAML (does
the strategy permit coexistence?), SQL (what does the migration drop?),
and a contract format (what does the old service version read?). The
engine could either (a) have each invariant parse and inspect the raw
formats it needs, or (b) have dedicated parsers translate every format
into one shared representation, with invariants reasoning only over that
representation.

## Decision

Build a normalized intermediate representation (`internal/ir`,
`architecture.md` §2) and require every invariant to consume only IR
types. Parsers (`internal/parser/*`) are the only code permitted to touch
raw YAML/SQL/contract text.

## Alternatives considered

**Direct rule checks on raw artifacts.** Each invariant owns its own
extraction logic against the raw formats it cares about (e.g. a "drop
column while old reader exists" check that greps SQL for `DROP COLUMN`
and cross-references a YAML file for the version). Rejected because:

- **Duplicated extraction logic.** Nearly every invariant needs "what does
  this migration change" and "what does this service read/write" — without
  an IR, that extraction is reimplemented, slightly differently, in every
  invariant, multiplying the surface area for parsing bugs.
- **Untestable in isolation.** A rule check coupled to raw-format parsing
  cannot be unit tested against a hand-constructed scenario without also
  exercising the parser; this collapses the three-layer test structure in
  `architecture.md` §9 into one layer, which is exactly what makes
  regressions hard to localize (a failing test doesn't tell you if the
  parser or the rule broke).
- **No shared vocabulary for diagnostics.** The counterexample model
  (`architecture.md` §11) needs to cite evidence uniformly across
  invariant families; without a shared IR, evidence citation format would
  need per-invariant, per-format special-casing.

## Consequences

- Every new artifact format requires a parser that produces IR values,
  which is upfront work before any invariant can use that format's facts.
- The IR must anticipate what invariants need (§2's per-field
  justification requirement in `architecture.md`); adding a field to
  support one invariant is normal and expected, not a smell.
- Parsers and invariants can be developed, tested, and reasoned about
  independently — a parser bug and an invariant-logic bug are always
  distinguishable by which layer's tests fail.
