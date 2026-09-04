// Package graph builds and traverses RolloutProof's transition graph
// (docs/architecture.md §3-4): the reachable RolloutStates a RolloutPlan
// can pass through, and the deterministic shortest path between any two
// of them.
//
// Construction is generative, not enumerative (docs/adr/0003): states
// are derived directly from the plan's own workload strategies and
// migration phases, never from a brute-force cross product of every
// theoretically possible combination.
//
// # Construction model
//
// Two independent dimensions are combined:
//
//  1. The "version lattice" — the cartesian product of each workload's
//     own linear progression (old-only -> [coexistence or transient
//     empty] -> new-only), one such progression per WorkloadChange in
//     the plan. Edges move exactly one workload forward by one step.
//
//  2. The "during-migration hypercube" — one boolean per
//     PhaseDuringRollout migration, true once that migration has
//     committed. All 2^D combinations are reachable nodes, because
//     nothing in a PhaseDuringRollout declaration orders a migration's
//     commit relative to any specific workload-progression event
//     (docs/architecture.md §3.3, assumption A2) — that ambiguity is
//     exactly the flagship hazard this project exists to catch. Edges
//     flip exactly one migration from pending to committed (monotonic:
//     migrations do not un-commit).
//
// PhaseBeforeRollout migrations are always committed (folded into the
// schema every node starts from); PhaseAfterRollout migrations are
// unambiguous by construction (the plan explicitly orders them after the
// rollout) and are appended as a linear chain of extra nodes after the
// point where every workload has reached its target version and every
// during-migration has committed — including the "rollout complete,
// migration still pending" node itself, which is what makes a live
// version's dependency on a not-yet-applied migration detectable
// (docs/scenario-corpus.md SC-UNSAFE-004/005).
//
// # Known scaling characteristic
//
// The version lattice is O(3^W) in the number of concurrently-changing
// workloads W, and the during-migration hypercube is O(2^D) in the
// number of PhaseDuringRollout migrations D in one plan. This is
// exponential, not the O(N×M) figure in docs/architecture.md §3.2's
// original sketch — implementation revealed that independent workloads
// and independent in-flight migrations genuinely can interleave in any
// order absent a declared dependency forcing otherwise (docs/adr/0003),
// and modeling fewer than all interleavings would silently drop
// reachable states. This is accepted for V1 because realistic rollout
// plans change a small, single-digit number of workloads and carry very
// few (usually one) in-flight migration per plan; docs/architecture.md
// §3.2 step 4's dependency-based pruning is the intended mitigation for
// larger plans and remains future work.
package graph
