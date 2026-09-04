# RolloutProof — Invariant Catalog

Read `architecture.md` first. Every invariant here is evaluated by
`internal/invariant` against the IR and transition graph defined there; no
invariant reasons over raw YAML/SQL. Every invariant ID is stable and
versioned — once shipped, an ID's meaning does not change; a corrected or
narrowed rule gets a new ID and the old one is deprecated, never silently
redefined, so that historical UNSAFE reports remain meaningful.

## Catalog format

Each invariant documents:

- **ID / Name**
- **Safety property** — the plain-language guarantee
- **Why it matters** — the real failure it prevents
- **Required evidence** — exact IR fields; absence of any of these is an
  automatic UNKNOWN for this invariant, per the field's own note
- **Evaluation algorithm** — pseudocode over IR types, run against every
  node in the transition graph reachable per `architecture.md` §3–4
- **Safe example / Unsafe example** — minimal artifacts
- **Counterexample structure** — how `architecture.md` §11's
  `Counterexample` is populated for this invariant specifically
- **Diagnostic structure** — the rendered text shape
- **Limitations** — what this invariant does *not* catch, even when it
  returns SAFE
- **Uncertainty behavior** — exactly which missing evidence produces
  UNKNOWN

## RP-DB — Database / Migration Safety

### RP-DB-001 — Destructive column removal while old readers remain

**Safety property.** A migration must not drop a column while any reachable
rollout state has a live service version that reads that column.

**Why it matters.** This is the flagship failure (vision.md §1): a rolling
update's coexistence window lets an old replica keep serving traffic after
a destructive migration commits.

**Required evidence.**
- `MigrationOp{Kind: OpDropColumn, Table, Column}` for the column being
  dropped.
- `Service.SchemaReads` for every `Service` version live in any reachable
  `RolloutState` (architecture.md §3.1), keyed by the same `Table`/`Column`.
- The `MigrationTiming.Phase` for the migration, to determine which states
  it applies against (architecture.md §3.2 step 3).

**Evaluation algorithm.**
```
for each RolloutState S in transition graph:
    if migration op (DropColumn on T.C) has been committed in S:
        for each service version v live in S:
            if ColumnRef{T, C} in v.SchemaReads:
                UNSAFE — evidence: this op, this v's SchemaReads entry,
                         this S's LiveVersions
report SAFE only if no such S found across ALL reachable states, and
SchemaReads was populated for every live service version encountered
(otherwise UNKNOWN, see below)
```

**Safe example.** `api` has a single version declared; its
`Service.SchemaReads` does not include `users.email`; migration drops
`users.email`. No reachable state has a reader of the dropped column →
SAFE.

**Unsafe example (the flagship scenario).** `api:v1.SchemaReads` includes
`users.email`. `api:v2` is deployed via `RollingUpdate`. Migration
`017_drop_email.sql` drops `users.email` with `Phase: PhaseDuringRollout`.
The `{api:v1, api:v2}` coexistence state, evaluated against the
post-migration schema (architecture.md §3.2 step 3), has `api:v1` live with
`users.email` in its `SchemaReads` → **UNSAFE**.

**Counterexample structure.**
- `Evidence`: the `OpDropColumn` (file + statement locator), the
  `api:v1.SchemaReads` entry citing the contract-metadata file that
  declared it.
- `Path`: the shortest sequence from rollout start to a violating state
  (architecture.md §4.4). For `PhaseDuringRollout`, implementation
  (`internal/graph`) shows this is **not necessarily**
  `[MigrationCommitted, NewPodReady]` (the coexistence path) — assumption
  A2 (architecture.md §3.3) makes the migration's commit point reachable
  at *any* point in the plan's timeline, including before any pod
  transition at all, so `[MigrationCommitted]` alone (one event, `api:v1`
  still the only live version) is frequently the shorter, and therefore
  selected, counterexample. This is a stronger finding, not a weaker one:
  it shows the hazard does not require coexistence to exist — coexistence
  is one way to reach an unsafe state in this scenario, not the only or
  shortest one. A report should not assume the rendered path always
  demonstrates coexistence specifically; it demonstrates whichever
  reachable violation is truly minimal.
- `RecommendedSequence`: derived generically (not hard-coded per scenario)
  by finding the smallest edit to the plan that removes the violating edge:
  here, "ship a version of `api` whose `SchemaReads` excludes the column,
  wait for full rollout, drop the column in a subsequent migration" — i.e.,
  the expand/contract pattern (see RP-DB-006).

**Diagnostic structure.**
```
RP-DB-001  UNSAFE
migration/017_drop_email.sql drops users.email
service api@v1 (still live during rollout) declares SchemaReads: users.email
  evidence: contracts/api.yaml declares api@v1 reads users.email
  evidence: deployment/api.yaml uses RollingUpdate — api@v1 and api@v2 may coexist
path: migration committed -> api@v2 pod ready (api@v1 still serving)
recommended sequence: deploy a compatibility release removing the read
  dependency; complete rollout; then apply the destructive migration
```

**Limitations.** Only catches the case where the dependency is declared.
Does not analyze application source code (vision.md §6). Does not model
whether the specific request path that reads the column is actually
exercised during the coexistence window (architecture.md A4) — it is
conservative: any declared read is treated as reachable.

**Uncertainty behavior.** If any live service version in a candidate state
has no declared `SchemaReads` entry for the affected table at all (i.e.,
the contract metadata doesn't mention the table), that version's
contribution is **UNKNOWN**, not assumed non-reading — the overall
invariant result for the run is UNKNOWN unless another live version is
independently found UNSAFE (aggregation rule, architecture.md §6).

---

### RP-DB-002 — Destructive column removal while old writers remain

Structurally identical to RP-DB-001, evaluated against `SchemaWrites`
instead of `SchemaReads`.

**Why it matters, distinctly from RP-DB-001.** A stale writer targeting a
dropped column fails outright (the column doesn't exist — the write
errors), whereas a stale reader may silently degrade (e.g., an ORM
returning a null/default). Both are failures, but they surface differently
in diagnostics and are tracked as separate invariant IDs so that a report
can distinguish "will hard-fail" from "may silently misbehave" —
`SchemaWrites` violations are always reported as hard failures in the
`Summary` text.

**Required evidence / algorithm / examples / counterexample / diagnostic /
limitations / uncertainty behavior.** As RP-DB-001, substituting
`SchemaWrites` for `SchemaReads` throughout.

---

### RP-DB-003 — Incompatible type change

**Safety property.** A migration must not narrow or incompatibly change a
column's type while any live service version reads or writes that column
using assumptions valid only under the old type.

**Why it matters.** E.g. `varchar(255)` → `varchar(50)` can truncate or
reject data an old writer still sends; `integer` → `bigint` is safe for
old readers (widening) but `bigint` → `integer` is not (narrowing can
overflow).

**Required evidence.**
- `MigrationOp{Kind: OpChangeType, Table, Column, NewType}`.
- The prior `Column.Type` from the `Schema` snapshot immediately before
  this op (architecture.md §2.3/§2.4).
- `SchemaReads`/`SchemaWrites` for live versions, as in RP-DB-001/002 —
  this invariant only fires for versions that actually touch the column.

**Evaluation algorithm.**
```
classify (OldType -> NewType) as Widening | Narrowing | Incomparable
  (a fixed, versioned compatibility table per Postgres type family —
  e.g. int->bigint: Widening; varchar(N)->varchar(M<N): Narrowing;
  text<->varchar: Widening; unknown pair: Incomparable)
if Widening: SAFE for this op (old readers/writers unaffected)
if Narrowing or Incomparable:
    for each RolloutState S with this op committed:
        for each live version v with T.C in v.SchemaReads or v.SchemaWrites:
            UNSAFE
```

**Safe example.** `integer` → `bigint` widening while `api:v1` writes the
column → SAFE (documented widening case).

**Unsafe example.** `numeric(10,2)` → `integer` while `api:v1` writes
fractional values to the column → UNSAFE (precision loss is classified
`Incomparable` by the fixed compatibility table, since the parser cannot
know whether existing/incoming data is fractional without deeper data
inspection RolloutProof does not perform in V1).

**Counterexample / diagnostic structure.** Same shape as RP-DB-001, with
`Evidence` additionally citing the type-compatibility classification used.

**Limitations.** The type-compatibility table is necessarily conservative
and Postgres-specific (vision.md §6); a type pair not in the table is
`Incomparable` by default (fails toward UNSAFE-eligible-for-review, not
SAFE) — this is a deliberate asymmetry from RP-DB-001's UNKNOWN-on-missing-
evidence default, justified because a compatibility table gap is a gap in
RolloutProof's own knowledge, not in the input evidence, so treating it as
"proceed to check readers/writers" is the safer failure direction than
skipping the check entirely.

**Uncertainty behavior.** If the prior `Column.Type` cannot be determined
(e.g. the column was added by an earlier `OpUnclassified` operation) →
UNKNOWN, since the compatibility classification itself cannot run.

---

### RP-DB-004 — Unsafe `NOT NULL` introduction

**Safety property.** Adding a `NOT NULL` constraint (via `OpSetNotNull` or
`OpAddColumn` with `Nullable: false` and no `Default`) must not occur while
a live writer version can supply rows without that column populated.

**Why it matters.** An old writer version, unaware of the new constraint,
inserts/updates a row without the column (or with `NULL`) → the write is
rejected by Postgres, a hard runtime failure for the old version, for as
long as it remains live.

**Required evidence.**
- The `MigrationOp` (`OpSetNotNull`, or `OpAddColumn` with
  `Nullable: false, Default: nil`).
- `SchemaWrites` for live versions targeting that table (not necessarily
  that exact column — an old version writing the *row* without knowing
  about the new column is the failure mode, so evidence is "does this
  version write to this table with a fixed/known column set that excludes
  the new column," which the contract metadata expresses as an explicit
  `SchemaWrites` entry per column the version's writes are declared to
  include).

**Evaluation algorithm.**
```
for each RolloutState S with the NOT-NULL op committed:
    for each live version v with any SchemaWrites entry on Table:
        if v.SchemaWrites does not include Column:
            # v writes rows to this table without this column
            Destructiveness := ConditionallyDestructive (ir §2.4)
            UNSAFE
```

**Safe example.** `OpAddColumn` with `Nullable: false, Default: "0"` — a
default satisfies old writers automatically (Postgres backfills/defaults
on write) → SAFE regardless of old writer declarations.

**Unsafe example.** `ALTER TABLE users ALTER COLUMN email SET NOT NULL`
while `api:v1.SchemaWrites` includes `users` rows without asserting
`email` is always populated, during `RollingUpdate` coexistence → UNSAFE.

**Counterexample / diagnostic structure.** As RP-DB-001, citing the
specific `SchemaWrites` gap rather than a `SchemaReads` overlap.

**Limitations.** Cannot distinguish "old writer never omits this column in
practice" from "old writer's contract metadata simply doesn't enumerate
it" — both look identical in the IR (vision.md §6, no source analysis).
This pushes many real-world safe-in-practice cases to UNSAFE or UNKNOWN
rather than SAFE, which is the intended conservative direction (vision.md
§10).

**Uncertainty behavior.** If a live version has no `SchemaWrites` entries
for the table at all (table not mentioned) → UNKNOWN for that version's
contribution, per the aggregation rule.

---

### RP-DB-005 — Rename without compatibility period

**Safety property.** `OpRenameColumn` must not be the *only* change — i.e.
a bare rename, with no compatibility period — while any live version
references the old name.

**Why it matters.** A bare rename is functionally a simultaneous drop of
the old name and add of the new one; every consequence of RP-DB-001/002
applies to the old name.

**Evaluation algorithm.** Treat `OpRenameColumn{Column: old, NewColumn:
new}` as an implicit `OpDropColumn{Column: old}` for the purposes of
RP-DB-001/002's algorithm — this invariant is implemented as a thin wrapper
that reclassifies the op and delegates, rather than a duplicated
algorithm, keeping the two invariant IDs distinct for reporting purposes
(a rename-caused failure should read as "RP-DB-005: rename" not "RP-DB-001:
drop", since the recommended remediation differs — see below) while sharing
one tested code path.

**Safe example.** Old and new column coexist (via a preceding
`OpAddColumn new` + application-level dual-write, modeled as two
migrations with an expand step first — see RP-DB-006) before the rename's
drop-equivalent step → SAFE.

**Unsafe example.** Single migration: `ALTER TABLE users RENAME COLUMN
email TO email_address` while `api:v1.SchemaReads` includes `users.email`
→ UNSAFE.

**Counterexample / diagnostic.** As RP-DB-001, with
`RecommendedSequence` specific to renames: "add `email_address` as a new
column, dual-write in application code, backfill, migrate readers to the
new name, then drop `email` in a separate migration" (the expand/contract
pattern, RP-DB-006).

**Limitations / Uncertainty.** As RP-DB-001/002 (delegated).

---

### RP-DB-006 — Expand/contract sequence violation

**Safety property.** A destructive change to a column/table that has an
in-progress expand/contract migration pattern (an additive "expand" step
followed by a later destructive "contract" step, a widely-used safe schema
change pattern) must not have its contract step ordered before every
consumer has migrated off the old shape.

**Why it matters.** Teams that *intend* to follow expand/contract can still
get the ordering wrong — e.g. shipping the contract migration in the same
release as the expand step, or before confirming rollout completion of the
step that stops using the old shape.

**Required evidence.**
- Two or more `MigrationOp`s on the same table/column-family, classified as
  an expand step (`OpAddColumn`) and a later contract step
  (`OpDropColumn`/`OpDropNotNull` reversal/etc.) referencing related
  names (RolloutProof matches by exact column-name-prefix/suffix
  heuristic *only* when the project's contract metadata explicitly links
  them via a declared `Migration` relationship — no name-guessing without
  that explicit link, since silent name-matching would be exactly the kind
  of unevidenced inference vision.md §6 rules out).
- The rollout state of every service version, to confirm all consumers
  have moved to the new shape before the contract step's phase.

**Evaluation algorithm.**
```
for each declared expand/contract pair (E, C):
    if C.Phase-relative-position is not after full rollout completion of
       every service version that had a declared dependency on E's target:
        UNSAFE — this is a sequencing violation independent of whether
        any single state individually violates RP-DB-001/002 (it may
        also independently trigger those; this ID exists to name the
        *sequencing intent violation* distinctly for diagnostic clarity)
```

**Safe example.** Expand step ships, full rollout completes (verified via
the transition graph reaching the all-new-version end state before the
next plan begins), contract step ships in a separate, later `RolloutPlan`
→ SAFE.

**Unsafe example.** Expand and contract steps are both included in the
same `Migration` file, or the contract step's `Phase` is
`PhaseDuringRollout` for the same rollout that also introduces the
consumer version change → UNSAFE.

**Limitations.** Requires the expand/contract relationship to be
explicitly declared (see Required evidence) — RolloutProof does not
attempt to infer intent from column naming alone.

**Uncertainty behavior.** If no explicit expand/contract relationship is
declared, this invariant does not fire at all (it is not applicable,
distinct from UNKNOWN) — RP-DB-001/002/003/004 still evaluate the contract
step independently on its own merits.

---

### RP-DB-007 — Irreversible migration with required rollback

**Safety property.** If a `RolloutPlan` declares a `RollbackTarget`, no
migration in the plan's forward path may be `Irreversible` (architecture.md
§5) unless the rollback target is defined to occur *before* that
migration's commit point.

**Why it matters.** This is the precondition check that feeds
RP-ROLLBACK's family (below) — it is listed under RP-DB because it is a
property of the migration itself (its `Reversibility`), evaluated
independent of any specific rollback attempt.

**Evaluation algorithm.**
```
if RolloutPlan.RollbackTarget != nil:
    for each Migration op committed before the rollback point in the
    forward transition graph:
        if op.Reversible == Irreversible:
            UNSAFE
        if op.Reversible == ConditionallyReversible:
            evaluate the data-written check (architecture.md §5) ->
            Unsafe if data has been written that the reversal would lose
```

**Safe / Unsafe examples, counterexample, diagnostics, limitations,
uncertainty.** See RP-ROLLBACK-001/002 below, which are the user-facing
instances of this check applied to specific rollback scenarios; RP-DB-007
is the shared underlying mechanism, documented once here and referenced
from `architecture.md` §5 rather than duplicated.

## RP-K8S — Kubernetes Rollout Mechanics

### RP-K8S-001 — Rollout permits incompatible versions to coexist

**Safety property.** If two versions of a service are `Incompatible`
(defined below) under the declared version scheme, the `RolloutStrategy`
must not create a coexistence window between them.

**Why it matters.** This is the *mechanism-level* precondition that every
RP-DB and RP-API invariant above implicitly depends on (coexistence is
what makes the old version's reads/writes/contract reachable at all); it
is named as its own invariant because a coexistence window can be unsafe
even absent a specific schema/API conflict RolloutProof can enumerate —
e.g. two versions declared mutually `Incompatible` in project config for
reasons outside RolloutProof's model (a documented escape hatch for
project-specific compatibility declarations that supplements, not
replaces, the derived checks).

**Required evidence.** `RolloutStrategy.Type`/`MaxSurge`/`MaxUnavailable`;
an `Incompatible` declaration between the two versions (from project
config or derived transitively from any other RP-DB/RP-API invariant
firing between them).

**Evaluation algorithm.**
```
if Strategy.Type == RollingUpdate and (MaxSurge > 0 or MaxUnavailable < Replicas):
    coexistence window exists
    if (oldVersion, newVersion) declared/derived Incompatible:
        UNSAFE
```

**Safe example.** `Recreate` strategy → no coexistence window → SAFE
regardless of compatibility declarations.

**Unsafe example.** `RollingUpdate` with `MaxSurge: 1` between two versions
project config explicitly marks `incompatible: true` (e.g. a breaking
protocol change with no other modeled evidence) → UNSAFE.

**Diagnostic structure.** Names the specific strategy fields that create
the window (`maxSurge`, `maxUnavailable`) alongside the compatibility
declaration's source.

**Limitations.** This is a coarse, explicit-declaration-driven check; most
real incompatibilities are caught more specifically by RP-DB/RP-API
(which independently derive coexistence from the same graph, per
architecture.md §3–4). RP-K8S-001 exists for compatibility facts outside
those specific families.

**Uncertainty behavior.** No `Incompatible` declaration and no other
invariant firing → this invariant reports SAFE for its own narrow check
(it has nothing to flag), which is correct because "no known
incompatibility" here is a closed-world fact about a declared, finite
config list, not an open-world fact about service behavior (contrast with
RP-DB-001's UNKNOWN-on-missing-`SchemaReads`, which is exactly why the
distinction matters and is documented here explicitly).

---

### RP-K8S-002 — Readiness admits traffic before dependencies are ready

**Safety property.** A `Workload` must not become `Ready` (and start
receiving traffic) before every workload it `DependsOn` is itself `Ready`
at a compatible version.

**Why it matters.** Kubernetes readiness probes check only that a pod's
*own* process is up; they say nothing about whether a dependency it needs
is available, unless the workload explicitly gates on it.

**Required evidence.** `Workload.DependsOn`; `ReadinessSpec.
WaitsOnDependencies` for the dependent workload.

**Evaluation algorithm.**
```
for each Workload W with DependsOn including D:
    if not W.Readiness.WaitsOnDependencies:
        for each RolloutState S where W is Ready and D is not Ready
        (or D is Ready at an incompatible version, per RP-ORDER):
            UNSAFE
```

**Safe example.** `WaitsOnDependencies: true` (an init container or
startup probe checking the dependency) → the state where W is ready before
D is never reachable → SAFE.

**Unsafe example.** `WaitsOnDependencies: false`, `D` still rolling out →
UNSAFE, since W's own readiness says nothing about D.

**Limitations.** RolloutProof trusts the declared boolean; it does not
inspect the actual readiness probe implementation (vision.md §6, no source
analysis) — a probe that claims to wait but doesn't is undetectable here.

**Uncertainty behavior.** If `ReadinessSpec.WaitsOnDependencies` is simply
absent from the parsed manifest (no probe declared at all, vs. explicitly
declared and not dependency-aware) → UNKNOWN, not assumed `false` —
absence of a probe is evidence of nothing about dependency-awareness,
whereas a positively parsed non-dependency-aware probe is evidence of
`false`. This distinction is why `HasReadinessProbe` and
`WaitsOnDependencies` are separate fields (architecture.md §2.2).

---

### RP-K8S-003 — Termination/drain assumptions conflict with destructive change

**Safety property.** A destructive migration's commit point must not
precede the full termination of old replicas, when old replicas' shutdown
behavior itself assumes the pre-migration schema (e.g. a `PreStop` hook
that flushes buffered writes assuming the old column exists).

**Why it matters.** Distinct from RP-DB-001/002 in that the risk window is
during *shutdown*, not steady-state serving — a `PreStop` hook or
in-flight request drain can run schema-dependent code after the migration
commits, even if the replica is no longer accepting *new* traffic.

**Required evidence.** `TerminationSpec.GracePeriodSeconds`,
`HasPreStopHook`; the same `SchemaReads`/`SchemaWrites` evidence as
RP-DB-001/002, applied to the *terminating* replica population
(`WorkloadReplicaState.OldTerminating`, architecture.md §3.1), not just the
ready population.

**Evaluation algorithm.** Structurally as RP-DB-001/002, but the state
predicate checks `OldTerminating > 0` (a replica draining in-flight
work, potentially past a migration commit point) rather than `OldReady`.

**Safe example.** `GracePeriodSeconds: 0`, no `PreStopHook` — old replicas
terminate immediately with no drain-time schema access → SAFE for this
invariant (RP-DB-001/002 still apply to the ready-serving window
independently).

**Unsafe example.** `HasPreStopHook: true` with a long
`GracePeriodSeconds`, old version declared to write the dropped column
during shutdown flush → UNSAFE.

**Limitations.** RolloutProof cannot inspect what a `PreStop` hook actually
does (vision.md §6); `HasPreStopHook: true` combined with the version's
declared `SchemaWrites` is the only signal available — a hook that doesn't
actually touch the schema produces a conservative false-UNSAFE, an
accepted tradeoff (vision.md §10).

**Uncertainty behavior.** As RP-DB-002 (delegated), applied to the
terminating population.

---

### RP-K8S-004 — Rollout parameters create unsafe compatibility window

**Safety property.** `MaxSurge`/`MaxUnavailable` values that widen the
coexistence window (more simultaneous old+new replicas, longer overlap)
must not be combined with any migration whose `Phase` is
`PhaseDuringRollout` and whose `Destructiveness` is `Destructive` or
`ConditionallyDestructive`, without the sequencing evidence RP-DB-006
requires.

**Why it matters.** This is a **defense-in-depth** check distinct from
RP-DB-001/002: it fires on the *shape of the rollout parameters combined
with migration phase* alone, even before checking specific
reader/writer overlap, so it can flag risky configurations for review even
when contract metadata is incomplete (in which case RP-DB-001/002 would
otherwise only report UNKNOWN).

**Evaluation algorithm.**
```
if MaxSurge > 0 and any Migration in plan has Phase == PhaseDuringRollout
   and Destructiveness in {Destructive, ConditionallyDestructive}:
    if not covered by an explicit expand/contract declaration (RP-DB-006):
        flag as UNSAFE (or, per project config, as a lower-severity
        "advisory" — see Limitations)
```

**Limitations.** This invariant is intentionally coarser and more
conservative than RP-DB-001/002; a project may configure it as advisory
(reported but not blocking) precisely because it can fire even when
RP-DB-001/002 independently return SAFE with full evidence. It exists to
catch the *pattern* (destructive-during-rollout) even under incomplete
metadata, trading precision for recall, which is documented explicitly so
its output is not confused with the higher-precision RP-DB findings.

**Uncertainty behavior.** Does not itself produce UNKNOWN — it is a
structural check over always-available `RolloutPlan`/`Migration` fields,
not over optionally-declared contract metadata.

## RP-API — API / Contract Compatibility

### RP-API-001 — Provider removes endpoint while old consumer remains

**Safety property.** An `APIContract`'s `Endpoint` must not be removed by a
new provider version while any live consumer version's `APIConsumes`
references it.

**Required evidence.** `APIContract.Endpoints` diffed between provider
versions; `Service.APIConsumes` for every live consumer version.

**Evaluation algorithm.** Structurally identical to RP-DB-001, substituting
endpoint-set membership for column membership, and consumer/provider
`Service`s for reader/writer versions.

**Safe example.** Endpoint retained across provider versions, or removed
only after every declared consumer's `APIConsumes` no longer references it
→ SAFE.

**Unsafe example.** Provider `v2` drops `DELETE /users/:id`;
`consumer:v1.APIConsumes` still references it; `RollingUpdate` permits
`provider:v1`/`provider:v2` coexistence — wait, the actual risk is the
*consumer* calling the endpoint regardless of which provider replica
answers, so the precise unsafe condition is: `DELETE /users/:id` removed
from provider's contract at the version that becomes live, while any live
consumer still calls it → UNSAFE.

**Limitations.** Contract *presence* only — RolloutProof does not verify
the contract metadata matches the actual deployed API (vision.md §6).

**Uncertainty behavior.** Missing `APIConsumes` declarations → UNKNOWN, per
the same rule as RP-DB-001.

---

### RP-API-002 / RP-API-003 — Incompatible request / response contract

**Safety property.** A provider version must not require a request field a
live consumer doesn't send (RP-API-002), or remove/rename a response field
a live consumer requires (RP-API-003).

**Evaluation algorithm.** Compares `Shape.Fields` (presence + `Required`)
between the consumer's expected shape (`APIConsumes` version) and the
provider's actual shape at the live version, per the same coexistence-state
walk as RP-API-001.

**Limitations.** V1 models field presence/requiredness only, not deep type
compatibility (architecture.md §2.5) — a field present under an
incompatible type is not caught by RP-API-002/003; this is a named,
explicit gap, not silently assumed safe (it is out of scope, distinct from
UNKNOWN: RolloutProof does not claim to evaluate type compatibility at all
here, the same way RP-DB-006 does not fire without an explicit
declaration).

**Uncertainty behavior.** Missing `Shape` data for either side → UNKNOWN.

---

### RP-API-004 — Mixed-version producer/consumer incompatibility

**Safety property.** The general form of RP-API-001/002/003: for any pair
of live provider/consumer versions in a reachable coexistence state, their
declared contracts must be mutually compatible.

This invariant exists as the aggregate check that also catches
n-way mixed version states (more than two versions of a service live at
once, e.g. a canary alongside an in-progress rolling update) which
RP-API-001/002/003 state per-pair but this one evaluates exhaustively
across all live pairs in a given state, deduplicating diagnostics that
would otherwise repeat per pair with the same root cause.

**Uncertainty behavior.** As RP-API-001/002/003, aggregated.

## RP-ORDER — Dependency / Rollout Ordering

### RP-ORDER-001 — Provider upgraded before consumer compatibility

**Safety property.** A provider service must not reach a live version that
breaks compatibility with a still-live consumer version, before that
consumer has a version capable of tolerating the new provider (i.e., the
consumer's `DependsOn.MinCompatibleVersion` for the provider must already
be satisfied by *some* version the consumer could run, and that consumer
version must be the one live, or later, at the time the provider changes).

**Required evidence.** `ServiceDependency.MinCompatibleVersion`; the
version-ordering scheme (architecture.md §2.1.1) to compare versions.

**Evaluation algorithm.**
```
for each RolloutState S:
    for each live consumer version c depending on provider p:
        if live p version in S violates c.DependsOn[p].MinCompatibleVersion:
            UNSAFE
```

**Uncertainty behavior.** If the version scheme is `opaque-unordered`
(architecture.md §2.1.1), this invariant cannot compare versions at all →
UNKNOWN, always — this is the canonical example of the documented
`opaque-unordered` fallback.

---

### RP-ORDER-002 — Consumer upgraded before provider support

Symmetric to RP-ORDER-001: a consumer must not reach a live version that
requires provider capability not yet live in any reachable provider
version.

---

### RP-ORDER-003 — Database mutation occurs at unsafe rollout phase

**Safety property.** A `MigrationTiming.Phase` of `PhaseDuringRollout` for
a `Destructive` operation requires either (a) an explicit expand/contract
declaration (RP-DB-006) or (b) an explicit project-config acknowledgment
that the operation is safe despite coexistence (an documented,
auditable override — never a silent default).

This is the ordering-family counterpart to RP-K8S-004, phrased from the
migration-timing side rather than the rollout-parameters side; the two
are evaluated independently and may both fire on the same underlying
plan, which is intentional (each names a different aspect of the same
underlying risk for diagnostic clarity, per architecture.md's principle
that diagnostics should be specific rather than generic).

**Implementation note.** As with RP-K8S-004, path (b) (project-config
acknowledgment) is unavailable in V1 (`docs/architecture.md` §7's
`internal/config` does not exist yet) — only path (a) is checked. Because
this invariant fires on the phase/destructiveness pattern alone
(unlike RP-K8S-004, it does not additionally require the workload's
strategy to widen the coexistence window), it is at least as
over-inclusive and is marked `ir.Diagnostic.Advisory = true` for the same
reason: it must not, by itself, veto the overall verdict alongside the
higher-precision RP-DB family. See `docs/FALSE_POSITIVES.md`.

## RP-ROLLBACK — Rollback Safety

### RP-ROLLBACK-001 — Target rollout can fail after an irreversible migration

**Safety property.** A `RollbackTarget` naming a workload version to
revert to must not be reachable-unsafe per RP-DB-007's check
(architecture.md §5): no `Irreversible` migration may sit between the
current committed schema state and the schema state the rollback target's
version was designed against.

**Evaluation algorithm.** Direct application of RP-DB-007.

**Safe example.** Rollback target precedes any irreversible migration in
the committed sequence → SAFE.

**Unsafe example.** Rollback requested after an `Irreversible`
`OpDropColumn` has committed, and the rollback target version's
`SchemaReads`/`SchemaWrites` includes the dropped column → UNSAFE.

**Diagnostic structure.** Explicitly separates "the migration cannot be
reversed" (a `Reversibility` fact) from "the old version needs the
reversed state" (a `Service` fact), since a team may still be able to
safely roll back the *application* even when the *migration* cannot be
undone, if the old version doesn't actually need the dropped structure —
see RP-ROLLBACK-002.

**Uncertainty behavior.** As RP-DB-007/RP-DB-001 (delegated evidence
requirements).

---

### RP-ROLLBACK-002 — Old version cannot run against new schema

**Safety property.** Distinct from RP-ROLLBACK-001: even where the
migration is technically reversible, if the *plan* does not actually
include reverting it (an operational-only rollback, architecture.md §5),
the rolled-back old version must still be evaluated against the
**current, un-reverted** schema.

**Evaluation algorithm.** Treat the rollback as a `WorkloadChange` in a new
`RolloutPlan` whose target `Schema` is the currently committed one (no
migration reversal), and run RP-DB-001/002/003/004 against it exactly as
for a forward rollout — rollback safety is not a special case; it is the
same invariant family evaluated on a different `RolloutPlan` (architecture.md
§5, opening paragraph).

**Uncertainty behavior.** Inherited from whichever RP-DB invariant is
delegated to.

---

### RP-ROLLBACK-003 — Rollback target violates compatibility invariant

**Safety property.** The general aggregation: a rollback is UNSAFE if
*any* invariant in this catalog, evaluated against the rollback's own
transition graph, returns UNSAFE. This is the top-level rollback verdict
referenced as `Counterexample.RollbackVerdict` in architecture.md §11 — not
a new checking algorithm, but the named aggregate result of running the
full catalog against the rollback plan.

## Notes on catalog evolution

- New invariants get new IDs; nothing here is final. The numbering within
  a family (e.g. `RP-DB-008`) is reserved for additions, not renumbering.
- An invariant family's shared "Limitations" and "Uncertainty behavior"
  text is deliberately repeated per-ID rather than factored into a single
  shared paragraph, because a reporting/diagnostic tool must be able to
  show each ID's full documentation independently (e.g. in a `--explain
  RP-DB-002` CLI mode) without needing to resolve cross-references at
  runtime.
