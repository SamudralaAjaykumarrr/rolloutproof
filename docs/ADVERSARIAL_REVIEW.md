# Adversarial review

This document is for a reviewer trying to **break** RolloutProof, not
learn how to use it. If you want a quickstart, read `README.md`. If you
want the full safety-property catalog, read `docs/invariants.md`. If you
want the honest list of what a SAFE verdict does and does not prove,
read `docs/FALSE_POSITIVES.md`. This document is the map between those
three: what was actually attacked, how, what was found, and how to
attack it further yourself.

## Project thesis

RolloutProof proves whether a proposed Kubernetes Deployment + PostgreSQL
migration + service/API contract rollout is SAFE, UNSAFE, or UNKNOWN
across every reachable mixed-version, partially-migrated intermediate
state — not just the before/after snapshot (`docs/vision.md` §1). The one
property every other decision in this codebase serves is: **missing
evidence must never be silently treated as SAFE** (`docs/vision.md` §10).
An UNSAFE or UNKNOWN result is a genuine, evidence-backed finding derived
from IR facts and the transition graph, never a string matched against a
scenario's name (`docs/adr/0006`).

## Modeled guarantees

Given complete, accurate contract metadata and migrations within the
supported SQL subset, the guarantees in `docs/FALSE_POSITIVES.md`'s
"What RolloutProof can actually prove" section hold across **every**
state the transition graph can reach for the plan as declared — not
merely the start and end states. That graph is built directly from
semantic facts (workload strategy, replica count, migration phase,
declared dependencies), not inferred from naming conventions or file
order.

## Non-guarantees and assumptions

See `docs/FALSE_POSITIVES.md` in full; the short version:

- RolloutProof verifies the **plan as declared**, not the running
  system. It trusts contract metadata; it does not verify that metadata
  against real traffic (`docs/adr/0004`).
- A plan that changes no schema and declares no rollback target is out
  of RP-DB-001/002/004's scope by design (`docs/scenario-corpus.md`
  SC-SAFE-004) — a hazard that predates this rollout entirely is a
  pre-existing production issue, not something *this* rollout introduces.
- SQL support is a constrained, versioned subset (`internal/parser/sql`'s
  package doc); unsupported syntax is a parse error (tool failure, exit
  3) or an `OpUnclassified` operation that forces every affected state to
  UNKNOWN — never silently ignored.
- V1's CLI convention supports one transitioning workload per plan, plus
  any number of `staticWorkloads:` (non-transitioning, for multi-service
  dependency checks) — `internal/graph` itself supports N simultaneously
  transitioning workloads generically (proven in
  `internal/graph/graph_test.go`'s
  `TestBuild_TwoSimultaneousWorkloadTransitions_FullLatticeReachable`),
  but nothing upstream of it constructs that input today.
- Version comparison for RP-ORDER falls back to a dotted-numeric scheme;
  opaque version strings (build hashes, tags) are honestly UNKNOWN, never
  guessed at (`internal/invariant/rpordertypes.go`).

## What this adversarial-review round actually did

"Adversarial review" in this codebase's git history is a standing
practice, not a single pass — every prior round found and fixed at least
one real defect (`git log --grep="Adversarial-review finding"`). This
round's specific method, in the order the primary question demands:

### 1. False-SAFE attack harness

`internal/invariant/adversarial_test.go` (`FuzzAdversarialRollout`) and
`internal/invariant/adversarial_api_test.go` (`FuzzAdversarialAPIOrder`)
generate valid `ir.RolloutPlan`s directly from semantic facts — random
schemas, migrations parsed through the real `internal/parser/sql.Parse`
(so classification is never reinvented in the harness), versions,
strategies, and declared service/API/order facts — run them through the
exact orchestration `internal/verify.Run` uses, and independently
re-derive each hazard class (column existence, NOT NULL introduction,
type compatibility, API endpoint/field compatibility, dependency
ordering) from the same facts, sharing no bookkeeping with the code
under test. A property violation is a hazard the oracle can derive that
the engine's own aggregate verdict reports SAFE despite.

**Result: one confirmed false SAFE, fixed.** `graph.BuildRollback`
constructed the rollback's own `RolloutPlan` with no `Migrations` at all,
so every node in its graph had an empty `SchemaState.CommittedOps` even
though `BaseSchema` correctly reflected the forward plan's resulting
schema. RP-DB-003 (type compatibility) and RP-DB-005 (rename without
compatibility) both key their entire violation scan off `CommittedOps`,
not the schema snapshot — unlike RP-DB-001/002/004, which check the
snapshot directly and so were unaffected. A type change committed
anywhere in the forward rollout was therefore invisible to
RP-ROLLBACK-002's re-evaluation, regardless of whether the rollback
target actually depended on the changed column. Fixed by threading the
forward plan's full `CommittedOps` trail (and `Indeterminate` flag) into
the rollback graph's baseline (`internal/graph/graph.go`'s `build`
helper); regression test:
`internal/invariant/rollback_plan_test.go`'s
`TestEvaluateRollbackPlan_UnsafeOldVersionAgainstChangedColumnType`.

After that fix: `FuzzAdversarialRollout` ran clean for 11.3M executions,
`FuzzAdversarialAPIOrder` for 5.5M — both are `go test -fuzz`-compatible
and re-runnable (see "Exact commands" below); the crash corpus entry that
found the bug is committed under `testdata/fuzz/` and replays as an
ordinary regression on every `go test ./...`.

### 2. Metamorphic / property testing

`internal/invariant/metamorphic_test.go` — seven properties, each
independent of any single invariant's implementation:

- Removing a declared dependency cannot introduce a new RP-ORDER hazard.
- Adding compatibility evidence cannot worsen a verdict.
- Draining fully before a destructive migration removes the
  coexistence-specific hazard.
- Removing evidence degrades a violation to UNKNOWN — never fabricates
  SAFE.
- Rollback safety is evaluated independently of forward safety
  (forward-SAFE, rollback-UNSAFE case).
- An unclassified SQL operation never lets the verdict read SAFE.
- Diagnostics are byte-identical regardless of `services` map
  construction order.

All seven hold against the current implementation.

### 3. State-space completeness

Every graph test prior to this round exercised exactly one transitioning
workload. `internal/graph/graph_test.go`'s
`TestBuild_TwoSimultaneousWorkloadTransitions_FullLatticeReachable` adds
two independently, simultaneously transitioning workloads and asserts
all 9 points of the resulting 3x3 lattice are reachable — including the
four-versions-live worst case where both services coexist at once — with
an exact node count (proving no impossible states are generated
alongside them) and a check that no workload's live set is ever empty
mid-progression. Combined with the pre-existing
`TestBuild_NoImpossibleStatesGenerated`, `TestBuild_Deterministic`, and
the reachability tests for RollingUpdate/Recreate/pending-migration
states, this is the state-space evidence this round adds.

### 4. Invariant catalog adversarial review

Every implemented `RP-*` invariant was re-read against its own
documented safety property, assumptions, and limitations
(`docs/invariants.md`) during this round, specifically hunting for
evasion cases. Findings:

- **RP-DB-003 / RP-DB-005 on the rollback graph** — the false-SAFE
  finding above.
- **RP-K8S-003 (termination/drain conflict)** — a prior round's finding
  (`363e997`): the Recreate-strategy false positive, now correctly
  UNKNOWN. Re-verified still correct this round.
- **RP-API-004 (aggregate)** — a prior round's finding (`4c76f14`):
  duplicated `MissingEvidence` entries. Re-verified still correct.
- No new evasion case survived the fuzz harness or the manual review for
  RP-DB-001/002/004/006, RP-ROLLBACK-001/002/003, RP-API-001/002/003,
  RP-ORDER-001/002, RP-K8S-001/002/004.
- The strongest positive case (highest-confidence SAFE), strongest
  negative case (clearest UNSAFE), and boundary/UNKNOWN case for each
  family are exactly the examples already in `examples/{safe,unsafe,
  unknown}/` and `docs/scenario-corpus.md` — this review re-ran the full
  corpus (`go run ./cmd/eval`) and confirmed all pass before and after
  every fix.
- Overlap/duplication between invariants is intentional and documented,
  not accidental: RP-DB-005 is a thin wrapper delegating to RP-DB-001/002's
  mechanism; RP-ROLLBACK-001 is RP-DB-007 evaluated against a specific
  declared target; RP-K8S-001 is a defense-in-depth echo of whichever
  invariant found a coexisting pair incompatible; RP-API-004/RP-ORDER-002
  are named aggregates, not new algorithms. Each says so in its own doc
  comment specifically so a reviewer does not mistake deliberate overlap
  for a bug.

### 5. SQL parser adversarial testing

`internal/parser/sql`'s existing `FuzzParse` (quoting, schemas, chained
`ALTER TABLE`, malformed input, unsupported syntax, rename sequences,
type changes, NOT NULL transitions, multiple statements, comments,
case variation) re-ran clean for 30.1M executions this round with zero
crashes. Unsupported syntax reliably produces a parse error (tool
failure) or an `OpUnclassified` operation (forces UNKNOWN downstream),
never a silently-accepted, misclassified operation.

### 6. Graph scale / explosion

`internal/graph`'s benchmark suite (`go test -bench=. ./internal/graph/...`)
measures real growth, not an estimate: `BenchmarkBuild_DuringMigrations`
shows `D=8` at ~3.7ms / 30.9K allocations growing to `D=12` at ~145ms /
793K allocations — the actual exponential blowup `maxGraphNodes` exists
to cap. `TestBuild_RejectsOversizedPlan` confirms the cap fires with a
clear, actionable error rather than hanging or exhausting memory.

### 7. Diagnostic quality

Every rendered diagnostic (text, JSON, and SARIF — see
`internal/report`) answers, for a representative UNSAFE case
(`examples/unsafe/drop-column-before-drain`, the flagship scenario):
what is unsafe (`Summary`), which artifact caused it (`Evidence[].Artifact`),
the exact reachable sequence (`Counterexample.Path`), what assumption
matters (the `rollout_strategy`-kind `Evidence` entry, e.g. "the
migration's declared phase does not order its commit relative to..."),
whether rollback can recover (`RollbackVerdict`), what ordering would
make it safe (`Counterexample.RecommendedSequence`), and — for
`examples/unknown/missing-service-metadata` — exactly what evidence is
missing and why (`MissingEvidence[].Field`/`.Reason`). No diagnostic in
this codebase renders a generic "something is wrong" message.

### 8. Realistic case studies

`internal/invariant/casestudies_test.go`, cases A/B/D/E (C is
`TestMetamorphic_RollbackUnsafeDespiteForwardSafe`, kept there since it
is also the property regression for independent rollback evaluation):

- **A** — provider v2 + destructive migration + old consumer remains +
  rollback target depends on the removed column: RP-DB-001 and
  RP-ROLLBACK-003 both fire from the same root cause.
- **B** — RollingUpdate coexistence + readiness admitting traffic before
  a declared dependency is ready + the provider's contract changing
  underneath the still-live old consumer: RP-K8S-002 and RP-API-001,
  two independent hazards in one plan.
- **C** — forward-SAFE, rollback-UNSAFE.
- **D** — the same destructive migration UNSAFE during rollout,
  confirmed SAFE by the real engine once phased after full rollout
  completion — an unsafe ordering with a safe alternative the engine
  itself verifies, not just asserts.
- **E** — a single ADD COLUMN migration (NOT NULL, no default) on
  manifests that otherwise look entirely ordinary, with no contract
  metadata for either service version: UNKNOWN, not an assumed-safe
  guess.

Every verdict above is produced by the real engine in the test itself,
not hand-typed.

### 10. Final internal red-team pass

Beyond the harness's own 16.8M combined fuzz executions: the CLI was
attacked directly with a nonexistent directory, an empty directory, a
file passed as a directory, no arguments, an invalid `--format`, an
unknown subcommand, and an unwritable `--output` path — every case
produces a clear error and exit code 3 (tool failure), never a false
SAFE/UNSAFE/UNKNOWN and never a panic. Text/JSON/SARIF output was
re-verified byte-identical across repeated runs. This is genuinely a
**self**-red-team: see "Ceiling of this document" below for what that
does and does not establish.

## How to construct a scenario

1. Copy `examples/safe/additive-column` (or the closest existing example
   to what you want to test) as a starting point.
2. Edit `deployment.yaml` (strategy, replicas, readiness, termination),
   `schema.yaml` (base schema), `rolloutplan.yaml` (migration phases,
   rollback target, expand/contract links), migration files under
   `migrations/`, and contract metadata under `contracts/`.
3. Run `go run ./cmd/rolloutproof verify <your-directory>` and read the
   output.
4. If you want to construct facts directly at the IR level (no YAML,
   fastest way to explore a hypothesis) — see any test in
   `internal/invariant/*_test.go` for the pattern: build `ir.Table` /
   `ir.Schema` / `ir.Workload` / `ir.Migration` / `ir.RolloutPlan` via
   their `New*` constructors, `graph.Build` the plan, then call
   `invariant.Evaluate` / `EvaluateRollbackPlan` / `EvaluateAPI` /
   `EvaluateOrder` / `EvaluateK8s` and `invariant.Aggregate` in the same
   order `internal/verify.Run` does.

## How to challenge a verdict

Ask, in order:

1. **Is the verdict actually wrong given the facts as declared**, or is
   it correct given the facts but the facts themselves don't match
   reality? RolloutProof trusts declared contract metadata
   (`docs/adr/0004`) — it cannot detect that a `SchemaReads` entry is
   stale. That is a metadata-authoring problem, not a verifier bug.
2. **Is there a genuinely reachable state the transition graph omits**,
   or a state it includes that cannot actually occur? Compare against
   `docs/architecture.md` §3-4's construction rules and
   `internal/graph`'s own tests.
3. **Is the specific invariant's documented safety property actually
   violated**, or does it not apply here per its own documented
   Limitations (`docs/invariants.md`)? A gap between "what a reviewer
   expected" and "what the invariant is documented to check" is a
   documentation/scope issue, not necessarily a bug — see the RP-DB-004
   scope discussion above for a worked example of this exact question.
4. If it's still wrong after all three: it's a real defect. Report it
   (see below).

## How to report false SAFE / false UNSAFE / false UNKNOWN

Open an issue using the "Break RolloutProof" template
(`.github/ISSUE_TEMPLATE/break-rolloutproof.yml`). At minimum, include:

- **False SAFE** (the most serious class): the exact plan (directory or
  IR construction), the specific reachable state and live version(s)
  involved, and why the declared facts entail a hazard the verdict
  misses. This is exactly the shape `internal/invariant/adversarial_test.go`
  hunts for — if you can express it as an `oracleHazards`-style check
  that fails, that is the strongest possible report.
- **False UNSAFE**: the exact plan, which invariant fired, and why its
  documented safety property does not actually apply to this case (cite
  the specific assumption from `docs/invariants.md` that you believe is
  wrong or over-broad).
- **False UNKNOWN**: the exact plan and which `EvidenceGap` you believe
  is unnecessary — i.e., evidence that *is* actually present in the
  input but the engine failed to use.

## Minimal reproduction format

Prefer the smallest input that still reproduces the issue. Two accepted
forms:

1. A trimmed `examples/`-style directory (delete every file/field not
   needed to reproduce).
2. A Go test in the shape of `internal/invariant/casestudies_test.go` or
   `internal/graph/graph_test.go` — construct the plan via IR
   constructors directly, run the real engine, assert the verdict you
   believe is wrong.

Either way, state the exact command or `go test` invocation and the
exact observed output.

## Exact commands

```bash
# Run everything this review depends on:
gofmt -l .
go vet ./...
go build ./...
go test ./...
go test -race ./...
go run ./cmd/eval

# Re-run the false-SAFE fuzz harnesses (each replays its committed
# corpus first, then searches for new failures for the given duration):
go test -fuzz=FuzzAdversarialRollout -fuzztime=60s ./internal/invariant/...
go test -fuzz=FuzzAdversarialAPIOrder -fuzztime=60s ./internal/invariant/...
go test -fuzz=FuzzParse -fuzztime=60s ./internal/parser/sql/...

# Benchmarks (graph scale, SQL parsing, invariant evaluation, end-to-end):
go test -bench=. -benchtime=3x -run='^$' ./...

# Determinism check (should print one hash, repeated):
BIN=$(mktemp -d)/rolloutproof && go build -o "$BIN" ./cmd/rolloutproof
for i in 1 2 3; do "$BIN" verify --format json examples/unsafe/drop-column-before-drain | md5sum; done
```

## Ceiling of this document

Everything above is internal self-review: the same project that built
the engine also built the harness attacking it, which is a real but
bounded form of adversarial pressure — a shared blind spot in the
project's own semantic model could produce a harness that agrees with
the engine for the wrong reason. This round's evidence supports
**EXTERNAL-REVIEW READY**: a skeptical outside reviewer has a real
harness, a real reviewer package, and a real issue template to work
with. It does not by itself support **STAFF/PRINCIPAL DISCUSSION
READY** — that grade requires an actual external reviewer to have used
this package, found something (or confirmed they couldn't), and had
that finding closed with a regression test, which by definition cannot
happen inside a single internal review pass.
