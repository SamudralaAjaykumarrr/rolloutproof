# ADR 0002: Transition graph over a simple sequential before/after model

## Status

Accepted

## Context

The simplest possible model of a rollout is "before state" and "after
state" — check invariants against each and call it done. This is what most
existing tools implicitly do (vision.md §3). RolloutProof's entire reason
to exist is catching defects that live *between* those two states, in
mixed-version coexistence windows and partially-applied migrations. The
question is how to represent "between."

## Decision

Model a rollout as a directed acyclic transition graph
(`architecture.md` §4): nodes are `RolloutState` snapshots, edges are the
concrete mechanical events (pod ready, pod terminated, migration op
committed) that move the rollout from one state to the next. Invariants
are evaluated against every reachable node, not just the start and end.

## Alternatives considered

**Simple sequential (before/after) model.** Evaluate invariants against
exactly two states. Rejected outright — this is the status quo every
existing linter already implements (vision.md §3), and it is structurally
incapable of expressing the flagship scenario: "old reader is live" is
true only in an *intermediate* state that a two-state model never
represents. This alternative was considered only to be explicitly
rejected as inadequate to the core thesis (vision.md §4), not as a
close competitor.

**A middle option — a fixed three-state model (before / mixed / after).**
Model exactly one intermediate "mixed" state generically, without a full
graph. Considered because it's simpler to implement than a graph and
would catch the flagship scenario. Rejected because it cannot represent:
- Multiple independent migration operations committing at different
  points relative to rollout progress (each needs its own position in the
  timeline, not one shared "mixed" bucket — architecture.md §3.2 step 1).
- Multi-service dependency ordering, where service A's mixed state and
  service B's mixed state are not simultaneous and their relative
  ordering matters (RP-ORDER family).
- Rollback as a distinct, separately-evaluated transition (architecture.md
  §5) — a fixed three-state model has no natural place to attach a second,
  independent state sequence for the rollback path.
- Deterministic, explainable shortest-counterexample selection
  (architecture.md §4.4) — with one fixed "mixed" state there is nothing
  to select a *shortest path* among; every diagnostic would have to point
  at the same generic bucket regardless of the actual causal chain.

## Consequences

- The engine must construct and traverse a graph rather than evaluate two
  fixed states, which is materially more implementation complexity.
- That complexity buys exactly the properties the vision requires:
  multi-migration timelines, multi-service ordering, a real place to
  anchor rollback as its own graph, and deterministic shortest-path
  counterexamples (architecture.md §4.3–4.4).
- Graph construction must stay generative, not enumerative (ADR 0003), or
  the complexity cost compounds into a state-explosion problem instead of
  buying explanatory power.
