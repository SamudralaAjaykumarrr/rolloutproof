# RolloutProof — Scenario Corpus

This corpus is the intended regression-test fixture set (`architecture.md`
§9, `tests/`): each scenario becomes a small artifact set (manifest +
migration + contract metadata) with an asserted expected result. None of
these are implemented yet — this document defines what "correct" means for
each, ahead of implementation, so that the implementation is built to pass
a pre-agreed bar rather than having its bar defined after the fact.

Every scenario cites the exact IR facts (`architecture.md` §2) that make it
SAFE/UNSAFE/UNKNOWN, so that a reviewer can check the *reasoning*, not just
the verdict.

Format per scenario: **ID**, **Artifacts**, **Initial state**, **Proposed
rollout**, **Expected result**, **Violated invariant** (if unsafe),
**Why**, **Expected diagnostic** (summary line only — full shape is in
`invariants.md`), **Expected rollback classification**.

## SAFE scenarios

### SC-SAFE-001 — Additive nullable column before code use

**Artifacts.** `deployment/api.yaml` (RollingUpdate, `api:v1`→`api:v1`,
unchanged); `migrations/010_add_phone.sql` (`ADD COLUMN phone
varchar(20) NULL`); `contracts/api.yaml` (`api:v1.SchemaReads`/`Writes`
does not mention `phone`).

**Initial state.** `api:v1` live, schema without `phone`.

**Proposed rollout.** Apply migration only; no service version change.

**Expected result.** SAFE.

**Why.** `OpAddColumn` with `Nullable: true` is `NonDestructive`
(architecture.md §2.4); no live version references the new column either
way, so no RP-DB invariant has a violating state to find.

**Expected diagnostic.** `SAFE — 0 invariants violated, 0 unknown`.

**Expected rollback classification.** Reversible (`OpDropColumn` on an
unused, newly-added column is a clean inverse).

---

### SC-SAFE-002 — Expand / migrate / contract sequence

**Artifacts.** Three sequential `RolloutPlan`s: (1) migration adds
`email_address` (expand), (2) deploy `api:v2` which dual-reads
`email`/`email_address` and writes both, full rollout to completion, (3)
migration drops `email` (contract) declared via explicit expand/contract
relationship metadata (`invariants.md` RP-DB-006).

**Initial state.** `api:v1`, schema has `email` only.

**Proposed rollout.** The three-phase sequence above, each a separate
plan; contract step's `Phase` is `PhaseAfterRollout` relative to phase 2's
full completion.

**Expected result.** SAFE at every phase.

**Why.** RP-DB-006's expand/contract check confirms the contract step is
sequenced after full rollout of the version that stopped depending on
`email`; RP-DB-001 independently confirms no live version reads `email` by
the time it is dropped.

**Expected diagnostic.** `SAFE` at each phase; phase 3's report explicitly
notes `RP-DB-006: expand/contract sequence verified`.

**Expected rollback classification.** Phase 1: reversible. Phase 3:
irreversible (data in `email` is gone), but rollback is not required since
phase 2 already completed and no version needs `email`.

---

### SC-SAFE-003 — Backward-compatible API expansion

**Artifacts.** `contracts/orders.yaml` — provider `orders:v2` adds a new
optional response field `discount_code` to `GET /orders/:id`; endpoint and
all previously-required fields unchanged.

**Initial state.** `orders:v1` live, consumer `checkout:v1` live.

**Proposed rollout.** `RollingUpdate` of `orders:v1`→`v2`; `checkout`
unchanged.

**Expected result.** SAFE.

**Why.** RP-API-003 compares required fields only; an added *optional*
field is not a removal or new requirement, so no consumer's expectations
are violated in either coexistence direction.

**Expected diagnostic.** `SAFE — RP-API-003 evaluated: no required-field
removal detected`.

**Expected rollback classification.** N/A (no schema migration involved);
operational rollback of `orders` to `v1` is safe by the same contract
check run in reverse.

---

### SC-SAFE-004 — Safe rolling deployment, no schema change

**Artifacts.** `deployment/api.yaml` only; version bump `api:v1`→`api:v2`
with no migration and no contract change.

**Expected result.** SAFE.

**Why.** No `Migration` present at all — every RP-DB precondition is
vacuously unsatisfied; RP-K8S-001 finds no declared/derived incompatibility
between the versions.

**Expected diagnostic.** `SAFE — no schema or contract changes in scope`.

**Expected rollback classification.** Reversible (pure code rollback, no
schema state to consider).

---

### SC-SAFE-005 — Safe old/new coexistence around an unrelated column drop

**Artifacts.** `migrations/014_drop_legacy_flag.sql` drops
`users.legacy_flag`; `contracts/api.yaml` declares `api:v1.SchemaReads`/
`Writes` with no reference to `legacy_flag` at all (it was already
unused).

**Expected result.** SAFE.

**Why.** RP-DB-001/002's evaluation over every live version finds no
`SchemaReads`/`Writes` entry for the dropped column — and critically, the
contract metadata *does* enumerate `users` columns the version touches
(so this is a closed-world absence, not a missing declaration), which is
what distinguishes this from an UNKNOWN case (see SC-UNKNOWN-001 below).

**Expected diagnostic.** `SAFE — RP-DB-001/002: no live version references
users.legacy_flag`.

**Expected rollback classification.** Irreversible (data loss on
`legacy_flag`), but no version depends on it, so rollback of the
*application* is unaffected.

---

### SC-SAFE-006 — Migration after incompatible replicas fully drain

**Artifacts.** `migrations/020_drop_ssn.sql` (`DROP COLUMN
users.ssn`), `Phase: PhaseAfterRollout`; `api:v1.SchemaReads` includes
`users.ssn`; `api:v2.SchemaReads` does not.

**Proposed rollout.** Deploy `api:v2` via `RollingUpdate`, full rollout to
`{api:v2}`-only state confirmed reached, *then* apply the migration in a
separate, later plan.

**Expected result.** SAFE.

**Why.** With `Phase: PhaseAfterRollout`, the migration is only evaluated
against the post-rollout, `api:v2`-only state (architecture.md §3.2 step
3); `api:v1` is never live at the same time as the dropped column's
absence.

**Expected diagnostic.** `SAFE — migration phase excludes coexistence
window (PhaseAfterRollout)`.

**Expected rollback classification.** Irreversible; rollback to `api:v1`
after this migration would independently trigger RP-ROLLBACK-002 (a
*separate*, later rollback plan would be UNSAFE) — this scenario itself
only evaluates the forward rollout.

---

### SC-SAFE-007 — Reversible deployment, no destructive schema change

**Artifacts.** `RollbackTarget` set to `api:v1`; no migrations in the plan
at all.

**Expected result.** SAFE (forward and rollback both).

**Why.** RP-DB-007 has no migrations to evaluate — vacuously satisfied;
rollback reduces to a pure `WorkloadChange` reversal.

**Expected diagnostic.** `SAFE — rollback target reachable with no
intervening schema changes`.

**Expected rollback classification.** Reversible.

---

### SC-SAFE-008 — Widening type change under coexistence

**Artifacts.** `migrations/022_widen_user_id.sql` (`user_id` `integer` →
`bigint`); `api:v1` and `api:v2` both write `user_id` during
`RollingUpdate` coexistence.

**Expected result.** SAFE.

**Why.** RP-DB-003 classifies `integer`→`bigint` as `Widening` via the
fixed compatibility table (`invariants.md` RP-DB-003) — old writers
producing `integer`-range values remain valid under the wider type.

**Expected diagnostic.** `SAFE — RP-DB-003: widening type change
(integer -> bigint)`.

**Expected rollback classification.** Conditionally reversible — reversible
only if no value written exceeds `integer` range; RolloutProof reports
this condition explicitly rather than asserting unconditional
reversibility.

---

### SC-SAFE-009 — `NOT NULL` introduction with a default

**Artifacts.** `migrations/025_require_status.sql`
(`ALTER TABLE orders ADD COLUMN status text NOT NULL DEFAULT 'pending'`);
`api:v1.SchemaWrites` on `orders` does not mention `status`.

**Expected result.** SAFE.

**Why.** RP-DB-004's safe-example path: a `Default` value means Postgres
supplies it for old writers that omit the column; `Destructiveness` for
this specific op is `NonDestructive` by construction (architecture.md
§2.4's note that a default satisfies old writers automatically).

**Expected diagnostic.** `SAFE — RP-DB-004: NOT NULL introduced with
default, no old-writer conflict possible`.

**Expected rollback classification.** Reversible (`DropColumn` on
`status`, no other version depends on it).

---

### SC-SAFE-010 — `Recreate` strategy eliminates the coexistence window

**Artifacts.** `deployment/batch-worker.yaml` (`Strategy.Type: Recreate`);
migration drops a column `batch-worker:v1` reads; `batch-worker:v2` does
not.

**Expected result.** SAFE.

**Why.** Per architecture.md §3.2 step 2, `Recreate` produces the sequence
`{old} → {} → {new}` with **no** `{old, new}` coexistence node at all; the
migration, regardless of `Phase`, is only ever evaluated against a state
where `batch-worker:v1` is not live once the new schema is committed
(architecture.md's ordering assumptions tie migration phase to rollout
progress, and `Recreate`'s full-stop guarantees old is down before new
starts).

**Expected diagnostic.** `SAFE — Recreate strategy: no coexistence state
reachable`.

**Expected rollback classification.** Irreversible (destructive drop), and
a rollback plan targeting `batch-worker:v1` after this migration would be
UNSAFE per RP-ROLLBACK-002 — noted here as the natural next scenario,
not evaluated in this one.

## UNSAFE scenarios

### SC-UNSAFE-001 — Flagship: drop column before old replicas drain

**Artifacts.** `deployment/api.yaml` (RollingUpdate,
`MaxSurge: 1`); `migrations/017_drop_email.sql` (`DROP COLUMN
users.email`, `Phase: PhaseDuringRollout`); `contracts/api.yaml`
(`api:v1.SchemaReads` includes `users.email`; `api:v2.SchemaReads` does
not).

**Initial state.** `api:v1` live, schema has `users.email`.

**Proposed rollout.** Deploy `api:v2`; apply migration during the
rollout.

**Expected result.** UNSAFE.

**Violated invariant.** RP-DB-001.

**Why.** The `{api:v1, api:v2}` coexistence state, evaluated against the
post-migration schema (migration phase `PhaseDuringRollout`), has
`api:v1` live with a declared read of the dropped column.

**Expected diagnostic.** As shown verbatim in `invariants.md` RP-DB-001's
Diagnostic structure section (this scenario *is* that example).

**Expected rollback classification.** Irreversible (data loss); a
rollback plan to `api:v1` after this migration is independently UNSAFE
per RP-ROLLBACK-001.

---

### SC-UNSAFE-002 — Rename column without compatibility layer

**Artifacts.** `migrations/030_rename_email.sql`
(`RENAME COLUMN email TO email_address`, single migration, no prior
expand step); `api:v1.SchemaReads` includes `users.email`.

**Expected result.** UNSAFE.

**Violated invariant.** RP-DB-005.

**Why.** A bare rename is a drop-of-old-name + add-of-new-name with no
compatibility period; `api:v1` still live during `RollingUpdate`
coexistence references the now-gone name.

**Expected diagnostic.** `RP-DB-005 UNSAFE — rename drops referenced
column name "email" with no compatibility period`.

**Expected rollback classification.** Irreversible under the executed
rename; recommended fix is the expand/contract pattern (add new column,
dual-write, migrate readers, drop old column separately).

---

### SC-UNSAFE-003 — Old writer against restrictive new `NOT NULL` schema

**Artifacts.** `migrations/032_require_email.sql`
(`ALTER TABLE users ALTER COLUMN email SET NOT NULL`, no default
possible); `api:v1.SchemaWrites` on `users` does not declare `email` as
always populated.

**Expected result.** UNSAFE.

**Violated invariant.** RP-DB-004.

**Why.** During coexistence, `api:v1` may insert/update `users` rows
without `email` populated; Postgres rejects the write once the constraint
is committed.

**Expected diagnostic.** `RP-DB-004 UNSAFE — NOT NULL added to
users.email while api@v1 writes users rows without asserting email is
populated`.

**Expected rollback classification.** Conditionally reversible (dropping
the constraint is safe unless rows have since been rejected/blocked in a
way the application already handled differently) — flagged as
`ConditionallyReversible`, not asserted safe.

---

### SC-UNSAFE-004 — New reader against schema not yet migrated

**Artifacts.** `deployment/api.yaml` deploys `api:v2` via
`RollingUpdate`; `api:v2.SchemaReads` includes `users.loyalty_tier`, a
column added by `migrations/040_add_loyalty_tier.sql` whose `Phase` is
`PhaseAfterRollout` (i.e., planned to run only *after* the rollout, not
before or during it).

**Expected result.** UNSAFE.

**Violated invariant.** RP-DB-001's underlying existence-check mechanism,
applied symmetrically: a live version's `SchemaReads` entry must exist in
the *currently committed* schema at every state where that version is
live, not only "must not be later removed." (Documented in
`invariants.md` RP-DB-001 as the same reachability check run against
column existence rather than deletion.)

**Why.** At the moment `api:v2` becomes live (both in the coexistence
state and in the eventual `{api:v2}`-only state, since the migration is
scheduled *after* rollout completion), `users.loyalty_tier` does not yet
exist in the committed schema — every read fails.

**Expected diagnostic.** `RP-DB-001 UNSAFE — api@v2 declares SchemaReads:
users.loyalty_tier, which does not exist in the schema committed at the
time api@v2 becomes live (migration phase: PhaseAfterRollout)`.

**Expected rollback classification.** Reversible (no destructive change
occurred; rolling back to `api:v1` requires no schema reversal) — but the
*forward* rollout itself is unsafe regardless of rollback safety.

---

### SC-UNSAFE-005 — New writer against schema not yet migrated

**Artifacts.** As SC-UNSAFE-004, but `api:v2.SchemaWrites` includes
`users.loyalty_tier` instead of `SchemaReads`.

**Expected result.** UNSAFE.

**Violated invariant.** RP-DB-002's existence-check mechanism (symmetric
counterpart to SC-UNSAFE-004, on the writer side).

**Why.** `api:v2` attempts to write a column that does not exist yet —
the write fails outright (a hard error, not a silent degrade), correctly
distinguished as the write-side variant per `invariants.md` RP-DB-002's
note on why reads and writes are tracked as separate IDs.

**Expected diagnostic.** `RP-DB-002 UNSAFE — api@v2 declares SchemaWrites:
users.loyalty_tier, which does not exist in the schema committed at the
time api@v2 becomes live`.

**Expected rollback classification.** Reversible.

---

### SC-UNSAFE-006 — Incompatible API response during mixed rollout

**Artifacts.** `contracts/orders.yaml` — provider `orders:v2` renames
response field `total` to `total_amount` on `GET /orders/:id`;
`checkout:v1.APIConsumes` requires `total`.

**Expected result.** UNSAFE.

**Violated invariant.** RP-API-003.

**Why.** During `RollingUpdate` coexistence of `orders:v1`/`v2`, requests
answered by `orders:v2` omit the `total` field `checkout:v1` requires.

**Expected diagnostic.** `RP-API-003 UNSAFE — orders@v2 response for GET
/orders/:id no longer includes required field "total" (renamed to
"total_amount"); checkout@v1 still requires "total"`.

**Expected rollback classification.** N/A (no schema migration); reverting
`orders` to `v1` is immediately safe.

---

### SC-UNSAFE-007 — Provider removes endpoint before consumers migrate

**Artifacts.** `contracts/orders.yaml` — `orders:v2` removes `DELETE
/orders/:id` entirely; `checkout:v1.APIConsumes` references it.

**Expected result.** UNSAFE.

**Violated invariant.** RP-API-001.

**Why.** Direct application of RP-API-001's algorithm: the endpoint is
gone from the live provider version while a live consumer still calls it.

**Expected diagnostic.** `RP-API-001 UNSAFE — orders@v2 removes DELETE
/orders/:id; checkout@v1 still calls it`.

**Expected rollback classification.** N/A (no schema migration).

---

### SC-UNSAFE-008 — Readiness accepts traffic prematurely

**Artifacts.** `deployment/api.yaml` — `api` depends on `auth` (declared
via `Workload.DependsOn`); `api`'s `ReadinessSpec.WaitsOnDependencies:
false` (readiness probe checks only `api`'s own HTTP health endpoint).

**Expected result.** UNSAFE.

**Violated invariant.** RP-K8S-002.

**Why.** A reachable state exists where `api` is marked Ready (and
receives traffic) while `auth` is not yet Ready (or is at an incompatible
version) — `api`'s own readiness probe provides no protection against
this, by its own declared configuration.

**Expected diagnostic.** `RP-K8S-002 UNSAFE — api readiness does not wait
on dependency "auth"; state exists where api is Ready and auth is not`.

**Expected rollback classification.** N/A (ordering issue, not a schema
issue); classification concerns rollback of the *dependency ordering*,
which is not modeled as reversible/irreversible in the RP-DB sense.

---

### SC-UNSAFE-009 — Destructive migration before rollout completion

**Artifacts.** `migrations/050_drop_inventory_count.sql`
(`Destructive`, `Phase: PhaseDuringRollout`); no expand/contract
declaration present linking it to any prior step; `RollingUpdate` with
`MaxSurge: 2`.

**Expected result.** UNSAFE (advisory or blocking, per project config —
see `invariants.md` RP-K8S-004's Limitations).

**Violated invariant.** RP-K8S-004 (and, if contract metadata is present
and complete, independently RP-DB-001/002 as well — this scenario is
deliberately constructed so RP-K8S-004 fires even before checking
specific reader/writer overlap).

**Why.** The combination of a widened coexistence window
(`MaxSurge: 2`) with an undeclared, `Destructive`,
`PhaseDuringRollout` migration is exactly the structural risk pattern
RP-K8S-004 exists to catch as defense-in-depth.

**Expected diagnostic.** `RP-K8S-004 UNSAFE — destructive migration
phased during rollout with MaxSurge=2 and no expand/contract declaration`.

**Expected rollback classification.** Irreversible.

---

### SC-UNSAFE-010 — Rollback requested after an irreversible migration

**Artifacts.** As SC-UNSAFE-001, plus a subsequent `RolloutPlan` with
`RollbackTarget: api:v1` issued after `017_drop_email.sql` has committed.

**Expected result.** UNSAFE (rollback plan specifically).

**Violated invariant.** RP-ROLLBACK-001 (via RP-DB-007).

**Why.** `OpDropColumn` on `users.email` is `Irreversible`; `api:v1`'s
`SchemaReads` includes `users.email` — restoring `api:v1` against the
current (migrated) schema reproduces SC-UNSAFE-001's exact failure, now
guaranteed rather than merely reachable, since there is no `api:v2` to
fall back to.

**Expected diagnostic.** `RP-ROLLBACK-001 UNSAFE — rollback target api@v1
requires users.email, dropped irreversibly by 017_drop_email.sql; no
DOWN migration can restore lost data`.

**Expected rollback classification.** Irreversible; this scenario's whole
point is reporting that fact as the *primary* finding, not a footnote.

---

### SC-UNSAFE-011 — Dependency rollout order violation

**Artifacts.** `contracts/services.yaml` — `checkout` declares
`DependsOn: {ServiceName: payments, MinCompatibleVersion: "v3"}`;
`payments` is being rolled from `v2` directly to `v4`, but `checkout` is
still at a version compiled against `payments` `v2` semantics (i.e.
`checkout`'s own `DependsOn` entry was authored for a future `checkout`
version not yet deployed).

**Expected result.** UNSAFE.

**Violated invariant.** RP-ORDER-001.

**Why.** The live `checkout` version's actual compatibility requirement
(`v2`, unstated but implied by it being the version that predates the
`MinCompatibleVersion: v3` declaration) is violated the moment `payments`
reaches `v3`/`v4` while old `checkout` is still live during `payments`'
own coexistence window.

**Expected diagnostic.** `RP-ORDER-001 UNSAFE — payments reaches v4 while
checkout@<live> (compatible only through payments@v2) remains live`.

**Expected rollback classification.** N/A directly (an ordering issue);
recommended sequence is to deploy the compatible `checkout` version first,
confirm full rollout, then proceed with the `payments` upgrade.

## UNKNOWN scenarios (tri-state demonstration)

These are not part of the "10 safe / 10 unsafe" count above; they exist to
make the tri-state model (`vision.md` §10, `architecture.md` §6) concrete
in the same corpus, and must be included in the eventual regression suite
alongside the SAFE/UNSAFE fixtures — an implementation that only tests
SAFE/UNSAFE paths cannot verify the uncertainty behavior that is this
project's central safety property.

### SC-UNKNOWN-001 — Missing contract metadata for a live version

**Artifacts.** `migrations/060_drop_referral_code.sql` drops
`users.referral_code`; **no** `contracts/*.yaml` entry exists for
`api:v1` at all (the file is missing, not merely silent on this column).

**Expected result.** UNKNOWN.

**Why.** RP-DB-001/002 cannot determine whether `api:v1` reads or writes
`referral_code` — per architecture.md §6, absence of evidence is reported
as `EvidenceGap`, never resolved to SAFE.

**Expected diagnostic.** `UNKNOWN — RP-DB-001/002: no contract metadata
found for service "api" at version "v1"; cannot evaluate schema access
against migrations/060_drop_referral_code.sql`.

**Expected rollback classification.** UNKNOWN, for the same evidentiary
reason.

---

### SC-UNKNOWN-002 — Opaque, unordered version scheme with an ordering-dependent invariant

**Artifacts.** Project config declares `versionScheme: opaque-unordered`;
`payments` upgrades from image tag `build-8841` to `build-9012` (no
declared order between them); `checkout.DependsOn.MinCompatibleVersion`
references `payments` versions.

**Expected result.** UNKNOWN (for RP-ORDER-001/002 specifically; other
invariants not dependent on ordering evaluate normally).

**Why.** `opaque-unordered` makes version comparison undefined by
declared project policy (architecture.md §2.1.1) — RolloutProof does not
guess an order from tag strings.

**Expected diagnostic.** `UNKNOWN — RP-ORDER-001: versionScheme is
opaque-unordered; cannot determine whether build-9012 is compatibility-
forward of build-8841`.

**Expected rollback classification.** Evaluated independently — ordering
uncertainty does not itself imply rollback uncertainty unless a
rollback-relevant invariant also depends on version ordering.

## Corpus summary

| Category | Count |
|---|---|
| SAFE | 10 (SC-SAFE-001..010) |
| UNSAFE | 11 (SC-UNSAFE-001..011) |
| UNKNOWN | 2 (SC-UNKNOWN-001..002) |
| **Total** | **23** |

Every scenario's "Expected diagnostic" line is a normative test assertion,
not illustrative prose — the implementation is done with a scenario when
its actual CLI output matches the cited invariant ID, verdict, and cited
evidence (exact wording of the human-readable message may evolve, but the
structured fields it is built from, per `architecture.md` §11, may not
silently diverge from what's asserted here without updating this
document first).
