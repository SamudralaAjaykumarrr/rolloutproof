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
// The RP-ORDER family (rporder.go) is evaluated via EvaluateOrder:
//
//   - RP-ORDER-001 / RP-ORDER-002: a live consumer's declared
//     ServiceDependency.MinCompatibleVersion for a provider is not
//     satisfied by whatever provider version is live in the same
//     reachable state. docs/invariants.md describes RP-ORDER-002 as
//     exactly RP-ORDER-001 evaluated from the other direction, so both
//     IDs report the same underlying scan (rpordertypes.go's
//     compareVersions is V1's self-contained version-ordering fallback,
//     since no internal/config package exists yet to declare
//     docs/architecture.md §2.1.1's project-wide version scheme).
//
// The RP-K8S family's highest-value rules (rpk8s.go) are evaluated via
// EvaluateK8s:
//
//   - RP-K8S-001: rollout permits a version pair to coexist that another
//     family already independently proved incompatible — V1 has no
//     project-config mechanism to declare compatibility facts outside
//     that, so this is currently a "derived transitively only" echo of
//     an existing finding (see EvaluateK8s's own doc comment).
//   - RP-K8S-002: a workload's readiness admits traffic before a
//     declared dependency is ready.
//   - RP-K8S-003: a destructive migration commits while a draining
//     replica with a PreStop hook may still run schema-dependent
//     shutdown code (V1 scoping: approximated via the coexistence state,
//     since internal/graph does not yet model a distinct terminating-
//     replica-population dimension — see evaluateTerminationConflict's
//     own doc comment).
//   - RP-K8S-004: a coarse, advisory (ir.Diagnostic.Advisory) structural
//     check — MaxSurge/MaxUnavailable widening coexistence combined with
//     an undeclared destructive migration during rollout — that trades
//     precision for recall by design and does not by itself veto the
//     overall rollout verdict (Aggregate).
//
// An invariant here never inspects raw YAML/SQL: it reasons only over
// ir.RolloutState, ir.CommittedOp, and ir.Service facts already lifted
// out of the source artifacts by the parser packages and the transition
// graph (docs/adr/0001).
package invariant
