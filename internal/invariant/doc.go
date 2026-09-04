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
//   - RP-DB-003: a live service version reads or writes a column whose
//     type a committed migration has changed incompatibly (typecompat.go's
//     fixed Widening/Narrowing/Incomparable table).
//   - RP-DB-004: a live service version writes rows to a table without
//     populating a column a committed migration has made NOT NULL.
//   - RP-DB-005: a live service version still reads or writes a column
//     name a committed migration has renamed away, with no compatibility
//     period.
//   - RP-DB-006: an explicitly declared expand/contract migration pair
//     whose contract step is not sequenced strictly after full rollout
//     completion (rpdb006.go; V1's single-plan scoping is documented on
//     evaluateExpandContractSequence).
//
// RP-DB-007 and the RP-ROLLBACK family (RP-ROLLBACK-001/002/003) are
// implemented in rollback_plan.go, evaluated separately via
// EvaluateRollbackPlan since they need plan.RollbackTarget and build
// their own transition graph (graph.BuildRollback) rather than reasoning
// over g.
//
// The RP-API family (rpapi.go) is evaluated separately via EvaluateAPI
// since it needs the api-contract registry (ir.APIContractKey ->
// ir.APIContract, internal/parser/contract.Registry.APIContracts) rather
// than just services:
//
//   - RP-API-001: a provider removes an endpoint a live consumer still
//     calls.
//   - RP-API-002: a provider requires a request field a live consumer
//     doesn't send.
//   - RP-API-003: a provider's response drops a field a live consumer
//     requires.
//   - RP-API-004: the aggregate of the three above across every live
//     provider/consumer pair in a state.
//
// RP-K8S-* and RP-ORDER-* are documented but not yet implemented; see
// the catalog's own "Notes on catalog evolution" for how new invariant
// IDs are added without disturbing existing ones.
//
// An invariant here never inspects raw YAML/SQL: it reasons only over
// ir.RolloutState, ir.CommittedOp, and ir.Service facts already lifted
// out of the source artifacts by the parser packages and the transition
// graph (docs/adr/0001).
package invariant
