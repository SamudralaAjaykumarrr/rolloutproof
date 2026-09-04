// Package ir defines RolloutProof's normalized intermediate representation.
//
// Every parser (internal/parser/*) translates one artifact format into
// these types; every invariant (internal/invariant) and the transition
// graph (internal/graph) reason only over these types, never over raw
// YAML/SQL. See docs/architecture.md for the design rationale.
//
// Types in this package hold no behavior beyond pure, deterministic
// construction, validation, and derivation (e.g. Schema.Apply). They do
// no file I/O and import nothing outside the standard library, so that
// every other package in the module may depend on ir without creating a
// cycle (docs/architecture.md §10).
//
// Zero-value safety: every enum in this package (Verdict, Destructiveness,
// Reversibility, OpKind, RollbackVerdict) is defined so its Go zero value
// means "unknown / not yet classified", never a benign or safe outcome. A
// forgotten field or a bug that leaves a value unset must never be
// silently interpreted as safe — see docs/adr/0008-zero-value-safety.md.
package ir
