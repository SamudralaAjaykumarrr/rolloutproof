// Package invariant evaluates RolloutProof's safety invariant catalog
// (docs/invariants.md) against a built transition graph
// (internal/graph), producing ir.Diagnostic results.
//
// V1 implements the RP-DB family's column-compatibility invariants:
//
//   - RP-DB-001: a live service version reads a column a committed
//     migration has dropped.
//   - RP-DB-002: a live service version writes a column a committed
//     migration has dropped.
//   - RP-DB-004: a live service version writes rows to a table without
//     populating a column a committed migration has made NOT NULL.
//   - RP-DB-005: a live service version still reads or writes a column
//     name a committed migration has renamed away, with no compatibility
//     period.
//
// Every other invariant in docs/invariants.md — RP-DB-003 (type
// compatibility), RP-DB-006/007 (expand/contract sequencing, rollback
// preconditions), RP-K8S-*, RP-API-*, RP-ORDER-*, and RP-ROLLBACK-* as a
// first-class evaluation of RolloutPlan.RollbackTarget against its own
// transition graph — is documented but not yet implemented; see the
// catalog's own "Notes on catalog evolution" for how new invariant IDs
// are added without disturbing existing ones.
//
// An invariant here never inspects raw YAML/SQL: it reasons only over
// ir.RolloutState, ir.CommittedOp, and ir.Service facts already lifted
// out of the source artifacts by the parser packages and the transition
// graph (docs/adr/0001).
package invariant
