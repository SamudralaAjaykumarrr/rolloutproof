# RolloutProof — Architecture

Read `vision.md` first. This document defines *how* RolloutProof realizes
that vision: the normalized intermediate representation (IR), the rollout
state model, the transition graph, the counterexample data model, parser
boundaries, and Go package structure. `invariants.md` builds on the types
defined here; it does not redefine them.

## 1. Design goal for the IR

The verification engine must never reason directly over YAML maps, SQL
token streams, or raw strings. Every parser's job is to translate one
artifact format into the shared IR defined below; every invariant's job is
to reason only over IR types. This separation is what makes invariants
testable independent of parsing, and parsers testable independent of
invariants (see `architecture.md` §9 Testing Hooks and the package layout
in §10).

Concretely: if an invariant implementation ever needs to `switch` on a YAML
node kind or regex-match a SQL string, that is a bug in the IR — the
relevant fact was not lifted into the IR where it belongs.

## 2. Normalized Intermediate Representation

All types below live in package `internal/ir`. Field lists are the minimum
needed for the invariant catalog in `invariants.md`; they are expected to
grow, but every field that exists must be traceable to a specific invariant
or diagnostic need — no speculative fields.

### 2.1 Service

```go
type Service struct {
    Name    string
    Version string // opaque identifier: image tag, semver, git SHA — see §2.1.1

    // Declared dependencies on other services, by name. Used for
    // RP-ORDER invariants.
    DependsOn []ServiceDependency

    // Schema access this service version declares. This is the
    // load-bearing fact for every RP-DB invariant: RolloutProof does not
    // infer this from source code (see vision.md §6).
    SchemaReads  []ColumnRef
    SchemaWrites []ColumnRef

    // API surface this service version exposes and consumes.
    APIProvides []APIContractRef
    APIConsumes []APIContractRef
}

type ServiceDependency struct {
    ServiceName        string
    MinCompatibleVersion string // opaque; compared via the versioning
                                 // scheme declared in project config, see §2.1.1
}

type ColumnRef struct {
    Table  string
    Column string
}
```

A `Service` value is a **version-scoped fact sheet**: "service X at version
V reads columns {A, B}, writes column {C}, provides API contract {D}."
Two versions of the same service (old and new) are two distinct `Service`
values connected by `Name`.

#### 2.1.1 Version comparison

RolloutProof does not assume semver. A project declares a **version
scheme** once, in its RolloutProof project config (`architecture.md` §7),
as one of:

- `semver` — standard precedence rules, compatibility via range
  constraints (e.g. `>=2.0.0`).
- `opaque-ordered` — an explicit total order given as a list in config
  (e.g. `[v1, v2, v3]`); useful for pre-semver projects or image tags with
  no numeric structure.
- `opaque-unordered` — no ordering is knowable; only equality is
  supported. Any invariant that requires ordering (e.g. "did the consumer
  upgrade before the provider") evaluates to **UNKNOWN**, not an assumed
  order, when `opaque-unordered` is declared. This is the deliberate,
  documented fallback for "we do not know" — see vision.md §11.

### 2.2 Kubernetes Workload

```go
type Workload struct {
    Kind        string // "Deployment" in V1; see vision.md §6
    Name        string
    Namespace   string
    ServiceName string  // links to a Service.Name
    Version     string  // links to a Service.Version (e.g. image tag)

    Replicas       int
    Strategy       RolloutStrategy
    Readiness      ReadinessSpec
    Termination    TerminationSpec

    DependsOn []string // other Workload names this one must be
                        // ready-before, if declared (see §2.1's
                        // ServiceDependency for the service-level version)
}

type RolloutStrategy struct {
    Type           string // "RollingUpdate" | "Recreate" — V1 models both;
                           // others (Argo canary, etc.) are UNKNOWN, see
                           // vision.md §6
    MaxSurge       IntOrPercent
    MaxUnavailable IntOrPercent
}

type ReadinessSpec struct {
    HasReadinessProbe bool
    InitialDelaySeconds int
    // Whether readiness is gated on a declared dependency being Ready.
    // Absence of this evidence means RolloutProof cannot rule out
    // "readiness admits traffic before dependencies are ready"
    // (RP-K8S-002) and must report UNKNOWN, not SAFE.
    WaitsOnDependencies bool
}

type TerminationSpec struct {
    GracePeriodSeconds int
    HasPreStopHook     bool
}
```

`Recreate` strategy is modeled because it changes reachability: it
eliminates the mixed-version coexistence window entirely (old is fully
terminated before new starts), which is exactly the fact several RP-K8S and
RP-DB invariants key on. Modeling it is what lets "safe rolling deployment"
scenarios in `scenario-corpus.md` be evaluated correctly rather than
assumed.

### 2.3 Database Schema

```go
type Schema struct {
    Tables map[string]Table
}

type Table struct {
    Name        string
    Columns     map[string]Column
    Indexes     []Index
    Constraints []Constraint
}

type Column struct {
    Name       string
    Type       string
    Nullable   bool
    Default    *string
}

type Index struct {
    Name    string
    Columns []string
    Unique  bool
}

type Constraint struct {
    Name string
    Kind string // "check" | "foreign_key" | "unique" | "primary_key"
    // Detail is intentionally loosely typed at this layer; specific
    // invariants that need FK target or check expression detail extend
    // this incrementally rather than the whole engine carrying fields
    // only one invariant uses.
    Detail string
}

// Schema.Apply(op MigrationOp) (Schema, error) is a pure function producing
// the post-migration schema; see §2.4. Schema values are immutable — the
// engine always holds a chain of Schema snapshots, one per migration
// boundary, never mutates in place.
```

`Schema` is deliberately minimal at V1: enough to answer "does this column
exist, and is it nullable" for every invariant in `invariants.md`. Index
and constraint detail exist because RP-DB invariants around unsafe `NOT
NULL` introduction and constraint changes need them; nothing is modeled
"in case it's useful later."

### 2.4 Migration

```go
type Migration struct {
    File string
    // Ordered: migrations apply operations in file order. Cross-file
    // ordering is determined by the project's migration-numbering
    // convention (declared in config — RolloutProof does not guess).
    Operations []MigrationOp
}

type MigrationOp struct {
    Kind OpKind
    Table string

    // Populated depending on Kind.
    Column       string          // add/drop/rename/type-change/set-not-null
    NewColumn    string          // rename target
    NewType      string          // type-change target
    Default      *string         // add-column default, if any

    Destructive    Destructiveness
    Reversible     Reversibility
    // Preconditions/postconditions this operation implies for schema
    // state — used by the transition graph to check migration ordering
    // against declared expand/contract sequences (RP-DB-006).
    Preconditions  []SchemaPredicate
    Postconditions []SchemaPredicate
}

type OpKind int
const (
    OpAddColumn OpKind = iota
    OpDropColumn
    OpRenameColumn
    OpChangeType
    OpSetNotNull
    OpDropNotNull
    OpAddIndex
    OpDropIndex
    OpAddConstraint
    OpDropConstraint
    OpUnclassified // parsed but not confidently classified — always
                    // drives evaluation to UNKNOWN, never a default
                    // Destructiveness/Reversibility
)

type Destructiveness int
const (
    NonDestructive Destructiveness = iota
    ConditionallyDestructive // e.g. SET NOT NULL: destructive only if
                              // existing NULLs are present or future
                              // writers may supply NULL — see
                              // invariants.md RP-DB-004/005
    Destructive
)

type Reversibility int
const (
    Reversible Reversibility = iota
    ConditionallyReversible // reversible only if no data has been
                             // written that depends on the change —
                             // see §5 Rollback Analysis
    Irreversible
)
```

`Destructiveness` and `Reversibility` are **derived by the SQL parser at
parse time** from the operation kind and its arguments (see §7), not
supplied by the user, and not inferred by the invariant engine at
evaluation time. This keeps the classification logic in one place and
independently unit-testable (`internal/parser/sql` tests, per §9).

### 2.5 API Contract

```go
type APIContract struct {
    Name            string
    ProviderService string
    ProviderVersion string
    Endpoints       []Endpoint
}

type Endpoint struct {
    Operation    string // e.g. "GET /users/:id"
    RequestShape Shape
    ResponseShape Shape
}

type Shape struct {
    // Fields present, each with a requiredness flag. V1 models presence
    // and requiredness only — deep type compatibility (e.g. int vs
    // string) is a documented limitation, see invariants.md RP-API
    // "Limitations".
    Fields []Field
}

type Field struct {
    Name     string
    Required bool
}

type APIContractRef struct {
    ContractName string
    Version      string // provider version this ref is compatible with
}
```

### 2.6 Rollout Plan

```go
type RolloutPlan struct {
    Workloads    []WorkloadChange
    Migrations   []MigrationTiming
    RollbackTarget *RollbackTarget // nil if no rollback under evaluation
}

type WorkloadChange struct {
    Workload Workload
    FromVersion string
    ToVersion   string
}

type MigrationTiming struct {
    Migration Migration
    // Phase relative to the workload rollout: applied before any new
    // pod starts, applied concurrently with the rollout (the common,
    // dangerous case — most CD pipelines run migrations as a step
    // before or during deploy, not gated on old-replica drain), or
    // applied after the rollout completes.
    Phase MigrationPhase
}

type MigrationPhase int
const (
    PhaseBeforeRollout MigrationPhase = iota
    PhaseDuringRollout
    PhaseAfterRollout
)

type RollbackTarget struct {
    Workload Workload
    ToVersion string
}
```

`RolloutPlan` is the top-level input the transition graph (§4) consumes: it
names *what* is changing and *when the migration commits relative to the
rollout*, which is exactly the fact that determines which intermediate
states are reachable.

## 3. Rollout State Model

### 3.1 What a state is

```go
type RolloutState struct {
    // Which Service versions have at least one live, traffic-receiving
    // replica. Zero, one, or two versions of the same service may be
    // present simultaneously (mixed-version coexistence).
    LiveVersions map[string][]string // service name -> live versions

    Schema SchemaState // which Schema snapshot is currently committed

    // Per-workload replica accounting, needed to distinguish
    // "old fully drained" from "old + new coexist" from "new only,
    // old still terminating."
    ReplicaState map[string]WorkloadReplicaState
}

type SchemaState struct {
    Snapshot Schema
    // Index into the migration's Operations that has been committed;
    // supports "migration partially applied" (failure-model.md).
    AppliedThroughOp int
}

type WorkloadReplicaState struct {
    OldReady, OldTerminating int
    NewReady, NewStarting    int
}
```

A `RolloutState` is a snapshot of "what versions of what are live, and what
schema is committed" at one point during a rollout. It is not a snapshot of
request-level runtime behavior (vision.md §11) — it is the state that
Kubernetes' and the database's own guarantees make *reachable*.

### 3.2 Controlling state explosion

The naive approach — enumerate every combination of {schema snapshot} ×
{live version set per service} × {replica counts} — is combinatorially
infeasible and mostly models impossible states (e.g., a schema snapshot
that no declared `MigrationTiming` ever produces). RolloutProof avoids this
by constructing states **generatively** from the `RolloutPlan`, rather than
enumeratively:

1. **The migration sequence and its declared `Phase`** produce an ordered
   list of committed-schema snapshots (`AppliedThroughOp` breakpoints). Only
   snapshots that a migration operation actually produces are ever states —
   there is no "guess an intermediate schema" step.
2. **The rollout strategy** determines which `LiveVersions` sets are
   reachable for a given workload:
   - `Recreate`: the only reachable sequence is `{old}` → `{}` (briefly,
     zero replicas) → `{new}`. No coexistence state exists.
   - `RollingUpdate`: `{old}` → `{old, new}` (coexistence, bounded by
     `MaxSurge`/`MaxUnavailable`, held for a duration RolloutProof treats
     as unbounded-but-real: see the reachability assumption below) →
     `{new}`.
3. **The cross product is taken only between the migration timeline and
   the rollout timeline**, anchored by `MigrationPhase`:
   - `PhaseBeforeRollout`: every rollout state is evaluated against the
     post-migration schema only. The pre-migration-schema × any-rollout-state
     combination is not reachable, because the plan defines the migration as
     already committed.
   - `PhaseDuringRollout`: both pre- and post-migration schema are
     evaluated against the coexistence state, because the plan gives no
     ordering guarantee between "migration commits" and "new pod starts
     receiving traffic" — this is precisely the flagship scenario, and it
     is why `PhaseDuringRollout` is the dangerous default most CD pipelines
     produce (vision.md §2.6).
   - `PhaseAfterRollout`: the coexistence and old-only states are evaluated
     against the pre-migration schema only. The new-only end state is
     evaluated against **both** schema snapshots, not the post-migration
     schema alone: the plan places the migration strictly after the
     rollout completes, but that still leaves a real window — "rollout
     finished, migration not yet run" — during which the fully-rolled-out
     new version is live against the old schema. Implementation
     (`internal/graph`) surfaces this window as its own node, distinct
     from the eventual fully-migrated target node reached one migration-
     commit edge later. Without it, a new version that depends on a
     column an after-rollout migration is about to *add* would be
     impossible to flag as UNSAFE — exactly `scenario-corpus.md`'s
     SC-UNSAFE-004/005, which is why this refinement exists.
4. **Multi-service ordering** (`ServiceDependency` / `Workload.DependsOn`)
   constrains which per-service state combinations are reachable at all:
   if service B declares a hard dependency on service A being at least
   version `V`, then any joint state with B-new and A-old is pruned as
   unreachable *only if* that dependency is declared; undeclared
   dependencies do not get this pruning and instead leave the relevant
   invariant's evaluation at UNKNOWN if it needed the ordering fact.
   **Not yet implemented** in `internal/graph` (see its package doc): V1's
   graph builder currently generates the full cross product of every
   workload's progression regardless of declared dependencies. This
   pruning is deferred, not abandoned — see the scaling note below.

This is the concrete mechanism referred to in `vision.md` §4: reachable
states are **derived from the rollout plan's own declared timing and
strategy**, not enumerated independent of it.

**Actual scaling characteristic (superseding this section's original
estimate).** Implementing `internal/graph` showed the `O(N × M)` figure
above understated the true state count: independent workloads and
independent in-flight (`PhaseDuringRollout`) migrations can interleave in
*any* order absent a declared dependency forcing otherwise, and modeling
fewer than all interleavings would silently drop reachable states — the
opposite of this project's safety direction (docs/adr/0003). The actual
construction is `O(3^W × 2^D)`: a cartesian product of each of `W`
concurrently-changing workloads' own 3-step progression (old-only,
coexistence-or-transient-empty, new-only), crossed with all `2^D`
commit/pending combinations of `D` `PhaseDuringRollout` migrations in the
same plan (`PhaseBeforeRollout` migrations are folded into the starting
schema; `PhaseAfterRollout` migrations are unambiguous by construction and
appended as a linear chain, not a cross product — see point 3 above). This
is still exponentially smaller than the `O(2^(N+M))` full-enumeration
alternative ADR 0003 rejects, and is accepted for V1 because realistic
plans change a small number of workloads and carry very few in-flight
migrations at once; item 4's dependency-based pruning is the intended
mitigation for larger plans and remains future work, tracked in
`internal/graph`'s package doc rather than implemented here.

### 3.3 Explicit reachability assumptions

Documented, not implicit:

- **A1 — Coexistence is real, not instantaneous.** During a
  `RollingUpdate`, the `{old, new}` state is treated as held for long
  enough that any request-serving old replica can receive and process at
  least one request. RolloutProof does not attempt to model rollout speed
  or request rate; it treats the coexistence window as *unsafe if any
  invariant is violated within it*, regardless of duration. This is
  intentionally conservative (vision.md §10): a fast rollout is not a safe
  rollout under this model, because production traffic is not proven safe
  by low probability.
- **A2 — A migration's `Phase` is authoritative.** RolloutProof trusts the
  declared `MigrationPhase` in the `RolloutPlan`; it does not attempt to
  infer actual CD pipeline behavior. If a project's real pipeline runs
  migrations at a different phase than declared, the verification is
  against the *declared* plan, and this is a data-accuracy problem for the
  integrating team's config, not a modeling gap — see §7 Project Config for
  how `Phase` is meant to be sourced directly from the CD pipeline
  definition to keep this honest.
- **A3 — Undeclared dependencies are not assumed absent.** The state model
  never removes a state from reachability because a dependency *wasn't*
  declared; it only prunes when one *was* declared. This is what keeps
  UNKNOWN from silently collapsing into SAFE (vision.md §10).
- **A4 — Replica-level races within the coexistence window (which specific
  request lands on which specific replica) are out of scope.** RolloutProof
  answers "can this state occur," not "how often will this state produce a
  failure." See `failure-model.md` for the explicit list of runtime/timing
  failures this excludes.

## 4. Transition Graph

### 4.1 Structure

```go
type TransitionGraph struct {
    Nodes []RolloutState
    Edges []Transition
}

type Transition struct {
    From, To int // indices into Nodes
    Event     TransitionEvent
}

type TransitionEvent struct {
    Kind EventKind // e.g. NewPodReady, OldPodTerminated, MigrationCommitted
    Detail string   // human-readable, for counterexample rendering
}
```

The graph is built directly by the generative process in §3.2: each
candidate state becomes a node; each step in the migration/rollout
timelines that produces the next candidate state becomes an edge. The
starting node is always the current, fully-old, pre-migration state; the
target node is always the fully-new, post-migration state described by the
`RolloutPlan`.

### 4.2 Ordering constraints

Edges are only added where the underlying mechanism actually permits the
transition:

- A `RollingUpdate` can only move `{old}` → `{old, new}` → `{new}` in that
  direction (Kubernetes does not un-start a surge pod without a
  terminating event); rollback is modeled as a **separate, explicit**
  `RolloutPlan` with `RollbackTarget` set, evaluated as its own transition
  graph starting from the state the rollout reached — not as a reverse edge
  on the forward graph. This mirrors reality: rolling back is itself a
  rollout with its own coexistence window, not an instantaneous undo (see
  §5 and RP-ROLLBACK in `invariants.md`).
- A migration's operations are only reachable in file order (§2.4);
  `PhaseDuringRollout` interleaves migration-commit edges with
  rollout-progress edges, but never reorders operations within one
  migration.

### 4.3 Deterministic traversal

The engine performs a breadth-first traversal from the start node,
visiting edges in a fixed, sorted order (by `EventKind` then lexically by
`Detail`) so that traversal order — and therefore counterexample discovery
order — is identical across runs on identical input, satisfying the
determinism requirement in `vision.md` §9. The graph is a DAG by
construction (§4.2's ordering constraints prevent cycles), so BFS
termination is guaranteed without a separate visited-state cutoff.

### 4.4 Counterexample selection

When more than one reachable node violates the same invariant, RolloutProof
reports the one reached by the **shortest path from the start node**
(fewest transition edges); ties are broken by the fixed edge ordering in
§4.3. This is what "smallest/useful unsafe counterexample" (vision.md,
problem statement) means concretely: the shortest causal chain from "the
rollout begins" to "the invariant is violated," not an arbitrary or
lexicographically-first violating state.

## 5. Rollback Analysis

Rollback is modeled as its own transition graph (§4.2), not a boolean flag,
because rollback safety depends on the same mixed-version-coexistence
mechanics as forward rollout — a rollback is a rollout in the opposite
version direction.

Definitions used throughout `invariants.md`'s RP-ROLLBACK family:

- **Reversible change**: a migration operation whose `Reversibility` is
  `Reversible` — an inverse operation exists that restores the prior schema
  state exactly (e.g. `AddColumn` ↔ `DropColumn` on a column with no
  dependent data written since).
- **Conditionally reversible change**: reversible only if no data has been
  written to the new/changed structure that would be lost or made invalid
  by reverting (e.g. reverting a `ChangeType` after new-typed data has been
  written may lose precision or fail to parse back). RolloutProof evaluates
  this by checking whether any workload state reachable *before* the
  proposed rollback point could have written such data — if so,
  conditionally-reversible collapses to **effectively irreversible** for
  that rollback target, not silently to reversible.
- **Irreversible change**: no inverse operation exists at all (e.g.
  `DropColumn` with no retained data — the data is gone regardless of
  whether the column is re-added).
- **Rollback compatibility**: whether the *old application version* being
  rolled back to can run correctly against the *current* (possibly
  already-migrated) schema. This is evaluated with the same `Service`
  schema-access facts used for forward-rollout invariants — an old version
  being restored is just another `Service` value in another
  `WorkloadChange`, evaluated by the same engine.
- **Migration rollback vs. operational rollback**: RolloutProof
  distinguishes reverting *application code* (an operational rollback,
  cheap and fast) from reverting a *committed migration* (a migration
  rollback, which most projects cannot do safely once new data has been
  written under the new schema). A `RollbackTarget` names which one is
  intended; RP-ROLLBACK invariants check operational rollback against
  current schema state by default, and explicitly flag when a proposed
  rollback would additionally require a migration rollback that the
  migration's `Reversibility` cannot support.

RolloutProof does not treat "a DOWN migration file exists" as sufficient
evidence of reversibility (vision.md's explicit instruction) — a DOWN file
is evidence only that an inverse *operation* was authored; whether applying
it is *safe given data written under the new schema* is the actual
question, and is answered by the reachability check above, not by file
presence.

## 6. Uncertainty Model

Every invariant evaluation produces one of:

```go
type Verdict int
const (
    Safe Verdict = iota
    Unsafe
    Unknown
)

type InvariantResult struct {
    InvariantID string
    Verdict     Verdict
    // Populated when Verdict == Unsafe.
    Counterexample *Counterexample
    // Populated when Verdict == Unknown: exactly what evidence was
    // missing, referencing the specific IR field/type that could not be
    // populated or compared. Never a generic "insufficient data."
    MissingEvidence []EvidenceGap
}

type EvidenceGap struct {
    Field       string // e.g. "Service.SchemaReads for api@v1"
    Reason      string // e.g. "no dependency metadata file declares
                        // schema access for this service version"
}
```

Aggregation rule across the whole run: the overall rollout verdict is
`Unsafe` if any invariant is `Unsafe`; otherwise `Unknown` if any invariant
is `Unknown`; otherwise `Safe`. This ordering — Unsafe dominates Unknown
dominates Safe — is what makes vision.md §10's "never silently treat
missing evidence as safe" mechanically true at the aggregate level, not
just at the per-invariant level.

## 7. Project Config

A `.rolloutproof.yml` (or discovered per-directory) declares facts
RolloutProof cannot derive from the artifacts themselves:

- Version scheme (§2.1.1).
- Migration file ordering convention (e.g. numeric prefix).
- Paths to service dependency-metadata files (§8).
- Default CI policy for UNKNOWN (vision.md §7): `block` (default) or `warn`.

Config is parsed into a plain Go struct (`internal/config`) and is itself
subject to the same "UNKNOWN over guessing" rule: a missing or ambiguous
config value that a specific invariant needs produces UNKNOWN for that
invariant, not a silently assumed default, with two narrow exceptions that
are documented in code and in `invariants.md` where they apply: CI policy
for UNKNOWN (defaults to `block`, the conservative direction) and migration
file ordering (defaults to lexical filename order, the universal
convention, but is overridable).

## 8. Parser Design

Four parser packages, one per input format, each translating into `ir`
types and nothing else:

- `internal/parser/k8s` — Kubernetes YAML → `ir.Workload`. Uses
  `sigs.k8s.io/yaml` + the upstream Kubernetes API types for `Deployment`
  so that structural validation is delegated to upstream, not
  reimplemented; RolloutProof's parser only extracts the fields in §2.2.
- `internal/parser/sql` — PostgreSQL migration SQL → `ir.Migration`. V1
  targets a **constrained, explicitly-supported subset** of DDL (the
  `OpKind` list in §2.4); any statement outside that subset parses to
  `OpUnclassified` rather than being silently ignored or guessed at. This
  is the explicit alternative to "pretending arbitrary SQL semantic
  analysis is solved" (vision.md §6): RolloutProof does not attempt to
  parse arbitrary procedural SQL (functions, triggers, `DO` blocks) in V1;
  such statements are `OpUnclassified` and drive UNKNOWN.
- `internal/parser/contract` — RolloutProof's own service
  dependency/contract metadata format → `ir.Service` and `ir.APIContract`.
  This is a YAML format RolloutProof defines itself (schema TBD in
  implementation, not architecture), because inferring schema
  reads/writes and API surface from arbitrary application source code
  is explicitly out of scope (vision.md §6). A team declares, per service
  version: tables/columns read and written, and API contracts
  provided/consumed. This is the one place V1 asks a human to assert a
  fact rather than deriving it — made explicit here rather than hidden
  behind an inference layer that would silently produce wrong answers on
  languages/frameworks it wasn't built for.
- `internal/parser/rolloutplan` — a RolloutProof-defined format (or CLI
  flags, in the simplest case) describing the `RolloutPlan` itself:
  which workloads change, migration phase, rollback target. In the
  simplest V1 CLI invocation this may be largely inferred from "diff of
  two manifest directories plus a migrations directory," but the
  `MigrationPhase` (§2.6) is exactly the kind of fact that must be
  explicit config, not inferred, since it is the crux of the flagship
  scenario — see A2 in §3.3.

Each parser package owns its own error types and is unit-testable against
fixture files with no dependency on the invariant engine (§9).

## 9. Testing Hooks (see `invariants.md` and the testing strategy)

The architecture is structured so that:

- Parsers are tested against fixture files → IR values, independent of
  invariants.
- Invariants are tested against hand-constructed IR values / transition
  graphs, independent of parsers.
- The scenario corpus (`scenario-corpus.md`) is the integration layer:
  real-shaped artifacts, through real parsers, through the real transition
  graph and invariant engine, asserting the documented expected verdict and
  (for UNSAFE) the documented counterexample shape.

This three-layer split is what makes "same input → same result" and "SAFE
must never contain a known violated invariant" (vision.md's testing
properties) checkable as actual automated tests rather than aspirations.

## 10. Package Layout

```
cmd/rolloutproof/        CLI entrypoint, flag parsing, output formatting
internal/ir/             IR types (§2, §3) — no parsing, no invariant logic
internal/parser/k8s/     Kubernetes YAML → ir.Workload
internal/parser/sql/     PostgreSQL migration SQL → ir.Migration
internal/parser/contract/ RolloutProof service/contract metadata → ir.Service, ir.APIContract
internal/parser/rolloutplan/ RolloutPlan assembly (§2.6)
internal/graph/          Transition graph construction & traversal (§3, §4)
internal/invariant/      Invariant catalog + evaluation engine (see invariants.md)
internal/report/         Verdict aggregation (§6), counterexample rendering (§Counterexample Model below)
internal/config/         Project config (§7)
examples/                Worked example artifact sets (paired with scenario-corpus.md)
tests/                   Scenario-corpus integration tests (§9)
```

Dependency direction is strictly downward: `cmd` depends on everything;
`report` depends on `invariant` and `graph`; `invariant` depends on `graph`
and `ir`; `graph` depends on `ir`; every `parser/*` depends only on `ir`;
`ir` depends on nothing else in the module. No package below `cmd` imports
`cmd`, and no `parser/*` package imports `invariant` or `graph` — a parser
that needs invariant-level logic to do its job is a sign a fact belongs in
the IR instead (§1).

Error handling: each package below `cmd` defines its own sentinel/typed
errors (e.g. `parser/sql.ErrUnsupportedStatement`); `cmd` is the only layer
that maps errors to process exit codes and human-readable messages.
`context.Context` is threaded through any function that does file I/O
(parsers, config loading) so the CLI can support cancellation/timeouts
later, but the pure computation packages (`ir`, `graph`, `invariant`) take
no `Context` — they are pure functions over already-loaded data, which is
also what makes them trivially fuzzable (see the testing strategy).

## 11. Counterexample Model (data structures)

```go
type Counterexample struct {
    InvariantID string
    Summary     string // one-line, e.g. "migration drops users.email
                        // while api:v1 still requires it"

    // The evidence directly cited, minimal and specific — not "all
    // input files," only the facts the invariant's precondition check
    // actually used.
    Evidence []EvidenceRef

    // The shortest violating path through the transition graph (§4.4),
    // rendered as an ordered event list for diagnostic output.
    Path []TransitionEvent

    RollbackVerdict Verdict // rollback safety of the state reached, if
                             // evaluated — nil/Unknown if not applicable

    // Present only when the invariant/state combination has a known
    // remediation pattern (not every invariant does — absence here is
    // not itself a defect).
    RecommendedSequence []string
}

type EvidenceRef struct {
    Kind string // "migration_op" | "schema_read" | "schema_write" |
                // "rollout_strategy" | "api_contract" | ...
    File string
    Locator string // statement index, YAML path, or similar —
                    // whatever the owning parser can supply
    Description string
}
```

This is the type that makes the worked example in the problem statement
(`migration applied → api:v2 starts → api:v1 replica remains live → ...`)
a rendering of `Counterexample.Path` plus `Counterexample.Evidence` plus
`Counterexample.RecommendedSequence`, produced mechanically by walking the
shortest violating path found in §4.4 and citing the IR facts each edge and
each invariant precondition actually consumed — never a hand-written string
matched against a scenario name. `internal/report` owns exactly one
rendering function per output format (text now; JSON/SARIF later, per
vision.md §7) that all consume this same struct, so adding an output format
never touches invariant logic.
