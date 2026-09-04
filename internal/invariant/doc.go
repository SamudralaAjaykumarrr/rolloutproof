// Package invariant evaluates RolloutProof's safety invariant catalog
// (docs/invariants.md) against a built transition graph
// (internal/graph), producing ir.Diagnostic results.
//
// V1 implements exactly the flagship database-compatibility pair:
//
//   - RP-DB-001: a live service version reads a column a committed
//     migration has dropped.
//   - RP-DB-002: a live service version writes a column a committed
//     migration has dropped.
//
// Every other invariant in docs/invariants.md is documented but not yet
// implemented — see the catalog's own "Notes on catalog evolution" for
// how new invariant IDs are added without disturbing existing ones.
//
// An invariant here never inspects raw YAML/SQL: it reasons only over
// ir.RolloutState, ir.CommittedOp, and ir.Service facts already lifted
// out of the source artifacts by the parser packages and the transition
// graph (docs/adr/0001).
package invariant
