# ADR 0003: Constrained reachability over full state enumeration

## Status

Accepted

## Context

Once a transition graph exists (ADR 0002), its nodes have to come from
somewhere. A `RolloutState` is a combination of {schema snapshot} ×
{live version set per service} × {replica counts per workload}. Naively,
one could enumerate every combination of these dimensions and let the
invariant engine discard the impossible ones during evaluation.

## Decision

Construct the graph **generatively** from the `RolloutPlan`'s own declared
migration timeline and rollout strategy (`architecture.md` §3.2): only
states that the plan's own mechanics can actually produce become nodes.
Reachability assumptions (A1–A4, `architecture.md` §3.3) are explicit and
documented, not implicit defaults baked into an enumeration-then-filter
pass.

## Alternatives considered

**Full state enumeration with post-hoc filtering.** Generate the full
cross product of every dimension's possible values, then discard states an
"impossibility" pass identifies. Rejected because:

- **Combinatorial cost with no bound tied to the actual plan.** The state
  space size would scale with the *number of possible values per
  dimension* (every schema snapshot ever seen, every version combination
  ever declared) rather than with the *actual complexity of the specific
  rollout under evaluation* — a plan with two workloads and one migration
  would pay the same enumeration cost as a plan with twenty of each,
  unless the impossibility filter itself becomes as complex as the
  generative approach, at which point nothing was saved.
- **The "impossibility" filter has to encode the same reachability
  assumptions anyway.** Something has to know that a `Recreate` strategy
  never produces a coexistence state, or that a migration with
  `PhaseBeforeRollout` is never evaluated against a pre-migration schema
  (architecture.md §3.2 step 3). Encoding that as a *filter over an
  already-generated superset* is strictly more code than encoding it as
  the *generation rule itself*, for the same result.
- **Silent, undocumented reachability assumptions are a determinism and
  trust risk.** vision.md §9 and §10 require that RolloutProof's
  reachability model be explicit and auditable. A filter-based approach
  tends to accumulate ad hoc exclusion rules over time, each locally
  justified, with no single place that states the full set of assumptions
  — exactly what `architecture.md` §3.3's A1–A4 list is designed to
  prevent by existing as one canonical, reviewable list.

## Consequences

- Every new reachability assumption must be added explicitly to
  `architecture.md` §3.3 and justified there — this is a deliberate
  friction point, not an oversight, because an undocumented reachability
  assumption is indistinguishable from a silent correctness bug in a tool
  whose entire value proposition is explainability.
- Graph size is `O(N × M)` in migration operations and workloads
  (architecture.md §3.2), not exponential, which keeps the engine's
  performance predictable and keeps traversal (ADR 0002's BFS) fast enough
  to run in CI without needing pruning heuristics beyond dependency-based
  pruning (§3.2 step 4).
- A gap in the generative rules (a real reachable state the generator
  fails to produce) is a false-SAFE risk distinct from an evidence gap —
  this is why the scenario corpus (`scenario-corpus.md`) exists as a
  standing check on the generator's completeness, not only on invariant
  logic.
