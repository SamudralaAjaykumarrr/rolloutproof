# RolloutProof — Failure Model

This document enumerates the real-world failure mechanisms a rollout can
encounter, and states plainly which ones RolloutProof's V1 state model
(`architecture.md` §3–4) can represent, which ones it approximates
conservatively, and which ones are out of scope. This is the honesty
document: it exists so that a reader can tell exactly what a SAFE verdict
does and does not promise.

Each entry: **mechanism**, **modeled?**, and if modeled, **how**; if not,
**why not / what would be required**.

## 1. Kubernetes-level failures

### 1.1 Partial rollout (rollout stalls partway through)

**Modeled: yes.** The transition graph's coexistence node
(`{old, new}` with specific `ReplicaState` counts, architecture.md §3.1) is
exactly this state, reached by any `RollingUpdate`. RolloutProof does not
distinguish "will stall" from "will complete quickly" (architecture.md A1)
— it treats the coexistence state as reachable and evaluates invariants
against it unconditionally, which is a superset of "partial rollout" as a
special case: a stall just means the window is held longer, and A1 already
treats the window as long enough to matter regardless of actual duration.

### 1.2 Old and new replicas simultaneously live

**Modeled: yes.** This is the core state the entire state model exists to
represent (architecture.md §3.1–3.2). It is the mechanism, not a failure —
RolloutProof's job is to check whether it's *safe*, not to flag its
existence as inherently bad.

### 1.3 Pod crash (new version crash-loops after deploy)

**Modeled: partially, as a reachability fact only.** RolloutProof does not
simulate crash-loop *timing* or predict whether a given deploy will crash.
What it does model: if a crash prevents the new version from ever becoming
`Ready`, the rollout does not progress past the coexistence state — which
is already a reachable state RolloutProof evaluates. RolloutProof does not
attempt to determine *whether* a crash will occur (that requires runtime
behavior, vision.md §11) — only that *if* the rollout stalls there
(for any reason, crash included), the coexistence-state invariants still
apply. Predicting the crash itself is out of scope.

### 1.4 Failed readiness probe

**Modeled: as RP-K8S-002's precondition.** Whether a *specific* probe will
pass or fail at runtime is out of scope (vision.md §11); what is modeled is
the *consequence structure*: a workload whose readiness doesn't gate on a
dependency (`ReadinessSpec.WaitsOnDependencies == false`) can reach a state
where it's marked Ready while a dependency isn't (RP-K8S-002). RolloutProof
does not predict whether the probe *will* fail — only whether the rollout's
declared readiness configuration is capable of preventing the unsafe state
if the dependency happens to be unready.

### 1.5 Delayed termination / long drain

**Modeled: yes, structurally.** `TerminationSpec.GracePeriodSeconds` and
`HasPreStopHook` feed RP-K8S-003 directly. RolloutProof does not model the
actual wall-clock delay a specific `PreStop` hook takes — only whether a
drain phase with schema-dependent behavior exists at all, treated as
unbounded-but-real per A1 (the same conservative treatment as the
coexistence window itself: a nonzero grace period plus a hook that
declares schema access is enough to make the state reachable, regardless
of actual duration).

### 1.6 Service dependency unavailable

**Modeled: yes, via `Workload.DependsOn` and RP-K8S-002/RP-ORDER.**
RolloutProof does not model *why* a dependency might be unavailable
(network partition, its own crash, etc.) — only whether the rollout plan's
declared readiness/ordering evidence is sufficient to prevent depending on
an unready or incompatible dependency, should unavailability occur.

### 1.7 Rollout mechanism failures RolloutProof does not model

- **Node-level failures** (node drain, node pressure eviction, OOM kill of
  the kubelet itself) — these affect *when* states occur, not *which*
  states are reachable in the sense RolloutProof cares about; a node
  eviction of an old replica just moves the rollout toward completion
  faster, it does not introduce a new kind of reachable state beyond what
  §1.1–1.2 already cover.
- **Admission webhook / mutating webhook side effects** that alter a
  manifest after RolloutProof has parsed it — RolloutProof verifies the
  artifact it is given; a cluster that mutates manifests post-verification
  is a gap between the verified artifact and the deployed one, which is a
  CI/pipeline integrity concern (ensuring RolloutProof runs against the
  *actual final* manifest) rather than something the verifier itself can
  detect.
- **HPA/VPA-driven replica count changes during rollout** — V1 models a
  fixed `Replicas` count from the manifest; autoscaling changing replica
  counts mid-rollout is not modeled. This can widen or narrow the actual
  coexistence window in ways RolloutProof's `MaxSurge`/`MaxUnavailable`-
  based reasoning doesn't capture. Documented gap; not silently assumed
  irrelevant — a future IR extension would need an explicit HPA/VPA input.

## 2. Database / migration-level failures

### 2.1 Migration partially applied

**Modeled: yes.** `SchemaState.AppliedThroughOp` (architecture.md §3.1)
represents exactly this — a schema snapshot mid-migration, one operation
at a time, per file order (architecture.md §2.4). RolloutProof evaluates
invariants at every such boundary, not only at "migration fully applied"
or "not yet applied."

### 2.2 Migration committed before deployment completes

**Modeled: yes — this is `MigrationPhase.PhaseDuringRollout` /
`PhaseBeforeRollout`** (architecture.md §2.6, §3.2 step 3), the load-bearing
distinction that produces the flagship UNSAFE scenario. This is the single
most important failure mechanism in the entire model.

### 2.3 Migration lock contention / long-running lock

**Not modeled in V1.** Postgres DDL operations (e.g. adding a `NOT NULL`
constraint without `NOT VALID`, or certain index operations without
`CONCURRENTLY`) can take an `ACCESS EXCLUSIVE` lock that blocks reads/writes
for the duration. This is a real, common production incident class, but it
is a *performance/availability* failure, not a *correctness/data-safety*
failure of the kind this catalog targets (vision.md §6, "not a general
correctness or QA tool"). A future invariant family (e.g. `RP-DB-LOCK-*`)
could check for missing `CONCURRENTLY`/`NOT VALID` patterns using the same
`MigrationOp` evidence already captured, but it is explicitly deferred, not
silently assumed handled.

### 2.4 Migration rollback (`DOWN` migration execution)

**Modeled: yes, per `architecture.md` §5.** RolloutProof does not execute
the DOWN migration; it evaluates whether applying it *would* be safe given
the `Reversibility` classification and any data-write evidence available.

### 2.5 Stale writers (old version writing under new schema assumptions)

**Modeled: yes — RP-DB-002, RP-DB-004.**

### 2.6 Stale readers (old version reading under new schema assumptions)

**Modeled: yes — RP-DB-001, RP-DB-003.**

### 2.7 Constraint/trigger side effects introduced by a migration

**Not modeled in V1.** `Constraint.Detail` (architecture.md §2.3) is
loosely typed and V1's invariant catalog does not evaluate check-constraint
expression semantics or trigger behavior — only presence/kind. A migration
that adds a `CHECK` constraint incompatible with old writers' data is not
caught unless it manifests as one of the modeled op kinds (e.g. an implicit
`NOT NULL`). Documented gap.

## 3. API / contract-level failures

### 3.1 API incompatibility during mixed-version rollout

**Modeled: yes — RP-API-001 through RP-API-004.**

### 3.2 Operator interruption (a human cancels/pauses a rollout mid-flight)

**Modeled: as a reachable, indefinitely-held coexistence state.**
RolloutProof does not model operator *intent* to pause — it already treats
the coexistence window as potentially unbounded (A1), so an operator pause
does not introduce a new state, only extends the duration of one already
evaluated.

### 3.3 Retry/restart behavior (client retries during a failing rollout)

**Not modeled in V1.** RolloutProof does not model client-side retry
semantics, backoff, or idempotency of retried requests against a
changing schema/API. This is a request-level runtime behavior
(vision.md §11, A4) — out of scope. A retried write during a coexistence
window is still just a "write from a live version," already covered by
RP-DB-002/004; what's specifically not modeled is retry-induced
double-write or ordering anomalies at the application level.

## 4. Explicit scope boundary summary

| Class | Modeled | Mechanism |
|---|---|---|
| Old/new coexistence | Yes | Transition graph nodes (architecture.md §3) |
| Migration timing vs. rollout | Yes | `MigrationPhase` (architecture.md §2.6) |
| Partial/stalled rollout | Yes (as unbounded coexistence) | A1 |
| Readiness/dependency ordering | Yes (declared evidence only) | RP-K8S-002 |
| Drain/termination schema access | Yes (structural, not timed) | RP-K8S-003 |
| Rollback safety | Yes | architecture.md §5, RP-ROLLBACK |
| Migration lock contention | No (deferred) | — |
| Constraint/trigger semantics | No (deferred) | — |
| Node-level infra failures | No (out of scope) | — |
| HPA/VPA replica dynamics | No (deferred) | — |
| Request-level retry/idempotency | No (out of scope) | — |
| Actual runtime request timing/rate | No (out of scope, by design — vision.md §11) | — |

A verdict of SAFE from RolloutProof means: *no invariant in the current
catalog, evaluated against every state reachable under the assumptions in
`architecture.md` §3.3, was found violated, and no evidence needed for
that evaluation was missing.* It does not mean the rollout cannot fail for
any reason — the "No" rows above are real production risks RolloutProof
does not yet claim to address, and this table is the canonical place that
says so.
