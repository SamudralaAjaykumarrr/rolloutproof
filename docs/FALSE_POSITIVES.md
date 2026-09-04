# Known precision boundaries

RolloutProof proves things about a **model** of your rollout — the
normalized IR built from a Kubernetes Deployment, a PostgreSQL
migration, and hand-authored contract metadata (`docs/architecture.md`
§1-§2) — not about your running system directly. A SAFE verdict means
"no evidence of a violation was found in the reachable states this model
can construct," never "this rollout cannot possibly fail in production."
This document says exactly where that gap is, so a SAFE result is never
mistaken for a stronger guarantee than it is.

## What RolloutProof can actually prove

Given complete, accurate contract metadata and a migration entirely
within the supported SQL subset (`internal/parser/sql`'s package doc):

- A live service version's declared `SchemaReads`/`SchemaWrites` will
  find every column it needs present in the schema at every reachable
  point in the rollout (RP-DB-001/002, the general existence-check
  mechanism — not only "was this column dropped").
- A column type change will not silently narrow or reinterpret a value
  an old reader/writer still assumes (RP-DB-003, against a fixed,
  documented compatibility table).
- A NOT NULL constraint will not reject a row an old writer still sends
  (RP-DB-004).
- A bare column rename will not break a reference to the old name
  (RP-DB-005).
- A declared expand/contract migration pair is sequenced correctly
  (RP-DB-006).
- A live API consumer's declared request/response field needs are met
  by every live provider version (RP-API-001/002/003/004).
- A live consumer's declared minimum-compatible-provider-version is
  satisfied by every live provider version, under a dotted-numeric
  version scheme (RP-ORDER-001/002).
- A workload's readiness does not admit traffic before a declared
  dependency is ready (RP-K8S-002).
- Rolling back to a named target version, without reverting any
  migration, will not hit a schema mismatch (RP-ROLLBACK-001/002/003).

Every one of these is a genuine, evidence-backed proof over the
reachable-state transition graph (`docs/architecture.md` §3-4) — not a
lint rule or a name-matching heuristic (`docs/adr/0006`).

## Where RolloutProof intentionally returns UNKNOWN

UNKNOWN is a real, positive answer ("I checked, and I don't have enough
evidence"), never silently collapsed to SAFE (`docs/vision.md` §10,
enforced by `ir.Verdict`'s dominance order — `internal/invariant.Aggregate`
never lets UNKNOWN lose to SAFE). It fires whenever:

- A live service version has no contract metadata file at all
  (`docs/scenario-corpus.md` SC-UNKNOWN-001).
- A live version's contract exists but a specific fact an invariant
  needs (a table's write columns, an API contract's shape) is genuinely
  absent, as opposed to positively declared empty.
- A migration operation falls outside the supported SQL subset
  (`OpUnclassified`) — every invariant downstream of that schema state
  becomes UNKNOWN, never assumed to be a no-op.
- A migration's prior column type cannot be determined (RP-DB-003).
- Two versions being compared use an opaque, non-dotted-numeric scheme
  (build hashes, git SHAs) that `internal/invariant`'s version
  comparator cannot order (`docs/scenario-corpus.md` SC-UNKNOWN-002).
- A consumer's declared API/service dependency names a provider not
  otherwise part of the plan being verified.
- RP-K8S-003 evaluates a draining-eligible workload (a PreStop hook with
  a nonzero grace period) whose strategy is `Recreate` or unrecognized —
  see "Known model gaps" below.

## Where rules are advisory, not high-confidence

**RP-K8S-004** is documented (`docs/invariants.md`) as deliberately
coarse: it fires on the *structural pattern* of a widened coexistence
window plus an undeclared destructive migration during rollout, even
when the higher-precision RP-DB family independently confirms SAFE with
complete evidence. It is marked `ir.Diagnostic.Advisory = true` and does
not by itself flip the overall verdict (`internal/invariant.Aggregate`)
— it is reported, never silently swallowed, but never conflated with a
blocking, high-confidence finding either. In the current example corpus
it fires on essentially every RollingUpdate deployment carrying any
`Destructive`/`ConditionallyDestructive` migration during rollout,
including migrations RP-DB-003 separately proves are a safe widening —
this over-firing is intentional (recall over precision, per its own
documentation), not a bug to silence.

**RP-ORDER-003** is the migration-timing-side counterpart to RP-K8S-004
(same rationale, same `ir.Diagnostic.Advisory = true` treatment): it
fires on any `PhaseDuringRollout` destructive migration with no declared
expand/contract sequencing, regardless of the workload's own strategy —
so it over-fires in the same cases RP-K8S-004 does, by design, and the
two are expected to co-occur on the same plan.

**RP-K8S-001** currently fires only by *deriving* from another
invariant's already-confirmed finding (see `internal/invariant`'s
`EvaluateK8s` doc comment) — it has no independent capability to declare
two versions "incompatible" absent a schema/API conflict RolloutProof
can already enumerate itself, since no project-config mechanism exists
yet for a team to declare that fact directly. It never produces a false
positive beyond whatever it derived from, but it also cannot yet catch
the class of incompatibility docs/invariants.md describes it existing
for ("declared mutually Incompatible... for reasons outside RolloutProof's
model").

## Known model gaps (possible false negatives)

- **RP-K8S-003 and Recreate.** `internal/graph`'s RollingUpdate model has
  a real coexistence state to anchor "old may be draining" to; Recreate
  has none — old fully stops before new starts, as a single edge, not a
  state. RolloutProof reports UNKNOWN for a draining-eligible Recreate
  workload rather than guess (see above) — meaning a genuine PreStop-hook
  hazard under Recreate is not caught, only honestly flagged as unproven.
- **No per-replica accounting.** `docs/architecture.md`'s
  `WorkloadReplicaState` (distinct `OldReady`/`OldTerminating`/`NewReady`/`NewStarting`
  counts) is not implemented; RolloutProof answers "can this version-set
  coexist," not "how many replicas of each." A hazard that depends on
  *how many* old replicas remain (not just whether any do) is out of
  scope.
- **No request-level modeling.** Assumption A4 (`docs/architecture.md`
  §3.3): RolloutProof answers "can this state occur," never "how often
  will a specific request land on a specific replica." A rollout that is
  reachable-but-rare in practice is still reported UNSAFE — this is a
  deliberately conservative choice (`docs/vision.md` §10: a fast rollout
  is not a safe rollout under this model), not an attempt to estimate
  real-world failure rates.
- **Constrained SQL subset.** `internal/parser/sql` recognizes a fixed
  set of `ALTER TABLE` forms; anything else (functions, triggers, `DO`
  blocks, most `CREATE`/`DROP TABLE`, multi-table statements) becomes
  `OpUnclassified` and drives UNKNOWN, never a guess. A migration that
  mixes a recognized statement with an unrecognized one loses schema
  tracking from the unrecognized statement onward
  (`ir.SchemaState.Indeterminate`).
- **No RP-DB-007 "conditionally reversible" data-write detection beyond
  declared facts.** Whether a `ConditionallyReversible` change actually
  had data written under the new structure is inferred from declared
  `SchemaWrites`, not from actually inspecting data — a service that
  writes the column without declaring it will not be caught here (it
  will, independently, produce its own UNKNOWN elsewhere for the missing
  declaration).
- **Single-workload-change CLI convention.** `internal/parser/rolloutplan`
  models one "primary" transitioning workload plus optional static
  (non-transitioning) ones per plan. Two workloads changing version
  *simultaneously* in one plan (rather than one changing while another
  is held static) is representable in `internal/ir`/`internal/graph`
  directly but not through the current CLI directory format — see
  `docs/architecture.md` §3.2 step 4's dependency-pruning note for the
  related, larger scaling concern this convention also sidesteps.
- **No project-config layer.** `docs/architecture.md` §7's
  `internal/config` (version scheme declaration, migration-ordering
  convention, CI policy for UNKNOWN) is not implemented. RP-ORDER's
  version comparison and RP-DB-006's expand/contract linking use
  self-contained, honestly-scoped fallbacks documented in code, rather
  than a project-wide policy a team declares once.

## What SAFE assumes

A SAFE verdict is conditioned on:

1. **The contract metadata is accurate.** RolloutProof does not infer
   schema/API access from source code (`docs/adr/0004`) — a service that
   reads a column its contract doesn't declare is invisible to every
   RP-DB/RP-API check, and SAFE will not catch it. This is the single
   largest practical source of a false SAFE, and it is a data-accuracy
   problem for the integrating team, not a modeling gap RolloutProof
   can close itself.
2. **The declared `MigrationPhase` matches the actual CI/CD pipeline
   behavior.** Assumption A2 (`docs/architecture.md` §3.3): RolloutProof
   trusts the declared phase; if the real pipeline runs a migration
   at a different point than declared, the verification is against the
   plan as declared, not reality.
3. **The migration is fully within the supported SQL subset** (or its
   unclassified portions correctly drove UNKNOWN rather than being
   silently ignored — verified true by construction, see
   `ir.SchemaState.Indeterminate`, but worth stating as an assumption
   a reader should not have to re-derive).
4. **No out-of-band schema/API change** happens outside what this
   specific `RolloutPlan` declares (a second team's concurrent,
   unrelated migration against the same database is invisible here).

None of this is a hedge to avoid — it is the precise, checkable boundary
of what "SAFE" means, stated once so it does not need re-deriving from
the source on every use.
