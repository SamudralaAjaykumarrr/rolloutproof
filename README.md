# RolloutProof

**Does this rollout actually stay safe on the way from here to there —
not just once it lands?**

RolloutProof verifies whether a proposed production rollout is safe —
not just whether the Kubernetes manifest and the SQL migration are each
individually valid, but whether the **transition** between the current
and target state is safe, including every mixed-version and
partially-applied intermediate state a real rollout passes through.

## The problem

Two individually valid deployment states can still have an unsafe
transition between them:

```
api:v1 still reads users.email
migration 017 drops users.email
Kubernetes rolling update permits api:v1 and api:v2 to coexist

migration applied
  -> old replica still receives traffic
  -> old replica queries removed column
  -> production failure
```

No single artifact here is wrong. `deployment.yaml` is valid. `017_drop_email.sql`
is valid. The *combination and timing* is wrong — and that's exactly the
class of defect that `kubectl apply --dry-run`, a linter, or a migration
tool's own safety checks cannot see, because none of them look across
the Kubernetes/database/service boundary at once.

## The thesis

RolloutProof derives this class of defect from a normalized model of
services, schemas, migrations, and rollout mechanics: it builds every
state the rollout can actually reach (not just "before" and "after"),
and checks a catalog of named safety invariants against every one of
them. It does not pattern-match this example, or any other, by name —
every verdict traces back to specific, cited evidence
(`docs/adr/0006`).

Every result is one of three states, never collapsed to two:

- **SAFE** — every implemented invariant evaluated, none violated.
- **UNSAFE** — a reachable state violates a named invariant, with a
  structured counterexample: the shortest path to it, the evidence
  cited, and (where derivable) a recommended fix.
- **UNKNOWN** — evidence is missing to reach a verdict either way. This
  is a first-class result, never silently treated as SAFE
  (`docs/vision.md` §10) — see `docs/FALSE_POSITIVES.md` for exactly
  when it fires.

## Architecture

```
 Kubernetes Deployment ─┐
 PostgreSQL migration ──┼──► parsers ──► normalized IR ──► transition graph
 contracts/*.yaml ──────┘  (internal/         (internal/ir)   (internal/graph)
                            parser/*)                              │
                                                                    ▼
                                                     invariant catalog (internal/invariant)
                                                       RP-DB · RP-ROLLBACK · RP-API
                                                       RP-ORDER · RP-K8S
                                                                    │
                                                                    ▼
                                                     structured Diagnostics
                                                          │        │        │
                                                          ▼        ▼        ▼
                                                        text      JSON    SARIF
                                                     (internal/report — cmd/rolloutproof)
```

Parsers only ever translate one artifact format into IR; invariants
only ever reason over IR types, never raw YAML/SQL
(`docs/architecture.md` §1). See that document for the full package
layout and the transition-graph construction model, and
`docs/invariants.md` for the invariant catalog.

## Install

```bash
go install github.com/SamudralaAjaykumarrr/rolloutproof/cmd/rolloutproof@latest
rolloutproof version
```

Or build from a clone, with real version metadata embedded
(`internal/version`):

```bash
git clone https://github.com/SamudralaAjaykumarrr/rolloutproof
cd rolloutproof
go build -ldflags "-X github.com/SamudralaAjaykumarrr/rolloutproof/internal/version.Version=$(git describe --tags --always) \
  -X github.com/SamudralaAjaykumarrr/rolloutproof/internal/version.Commit=$(git rev-parse --short HEAD) \
  -X github.com/SamudralaAjaykumarrr/rolloutproof/internal/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o rolloutproof ./cmd/rolloutproof
./rolloutproof version
```

No network access, container runtime, or live cluster/database
connection is required to build, test, or run RolloutProof — it is a
local, offline, read-only CLI.

## 2-minute quickstart

```bash
git clone https://github.com/SamudralaAjaykumarrr/rolloutproof
cd rolloutproof
go build -o rolloutproof ./cmd/rolloutproof

./rolloutproof verify examples/safe/additive-column          # exit 0
./rolloutproof verify examples/unsafe/drop-column-before-drain  # exit 1
./rolloutproof verify examples/unknown/missing-service-metadata # exit 2
```

Every command below was run against this repository's own `examples/`
fixtures to produce the output shown — nothing here is hand-written or
simulated.

### SAFE example

```
$ rolloutproof verify examples/safe/additive-column
ROLLOUT: SAFE

21 invariant(s) evaluated: 21 safe, 0 unsafe, 0 unknown
$ echo $?
0
```

A purely additive migration (`ADD COLUMN phone varchar(20)`), where no
live service version declares any dependency on the new column either
way (`examples/safe/additive-column`).

### UNSAFE example

```
$ rolloutproof verify examples/unsafe/drop-column-before-drain
ROLLOUT: UNSAFE

21 invariant(s) evaluated: 18 safe, 3 unsafe, 0 unknown

------------------------------------------------------------
RP-DB-001  UNSAFE

api@v1 still reads users.email, which does not exist in the schema committed
in this reachable state (migration "migrations/017_drop_email.sql" drops
users.email)

Evidence:
- migrations/017_drop_email.sql: migration "migrations/017_drop_email.sql"
  drops users.email
- contracts/api-v1.yaml: api@v1 declares it reads users.email
- the migration's declared phase does not order its commit relative to
  {api@v1} becoming live, so this state is reachable even without version
  coexistence

Counterexample:
1. migration "migrations/017_drop_email.sql" commits
2. runtime failure: the live version's declared schema access no longer
   matches the committed schema

Rollback: UNSAFE

Recommended sequence:
1. deploy a compatibility release of api that removes the dependency on
   users.email
2. wait for the compatibility release to complete its rollout
3. verify no live version still declares a dependency on users.email
4. apply migration "migrations/017_drop_email.sql"

[RP-ORDER-003 and RP-K8S-004 advisory findings omitted here — see full output below]
$ echo $?
1
```

This is the flagship scenario from "The problem" above, verified for
real (`examples/unsafe/drop-column-before-drain`). Note the
counterexample's shortest path is **one event** ("migration commits"),
not the two-event coexistence story you might expect — the migration's
declared phase makes that state reachable independent of whether a
second version is even coexisting, which is a stronger, more precise
finding, not a weaker one (`docs/invariants.md` RP-DB-001).

### UNKNOWN example

```
$ rolloutproof verify examples/unknown/missing-service-metadata
ROLLOUT: UNKNOWN

21 invariant(s) evaluated: 17 safe, 2 unsafe, 2 unknown

------------------------------------------------------------
RP-DB-001  UNKNOWN

RP-DB-001: insufficient evidence to evaluate every reachable state

Missing evidence:
- Service api@v1: no contract metadata file found for this service version
- Service api@v2: no contract metadata file found for this service version

[...]
$ echo $?
2
```

Same shape of migration as the UNSAFE example, but no
`contracts/*.yaml` file exists for `api` at all — RolloutProof cannot
determine whether the live version depends on the dropped column, and
says so specifically, rather than assuming it's fine.

### Cross-layer example

A migration commits irreversibly, and a later rollback plan targets the
version that depended on what it removed — the chain runs across the
schema, rollback, and Kubernetes-mechanics layers in one verification:

```
$ rolloutproof verify examples/unsafe/rollback-after-irreversible-drop
ROLLOUT: UNSAFE

21 invariant(s) evaluated: 13 safe, 8 unsafe, 0 unknown

------------------------------------------------------------
RP-DB-001  UNSAFE
api@v1 still reads users.email, which does not exist in the schema
committed in this reachable state (migration "migrations/017_drop_email.sql"
drops users.email)
Rollback: UNSAFE

------------------------------------------------------------
RP-DB-007  UNSAFE
an irreversible (or data-losing) migration committed before the rollback
point, which the rollback target depends on

------------------------------------------------------------
RP-ROLLBACK-002  UNSAFE
the rollback target cannot run correctly against the current, un-reverted
schema

------------------------------------------------------------
RP-ROLLBACK-003  UNSAFE
overall rollback classification is UNSAFE (RP-ROLLBACK-001: UNSAFE,
RP-ROLLBACK-002: UNSAFE)
Rollback: UNSAFE

------------------------------------------------------------
RP-K8S-001  UNSAFE
the rollout strategy permits a version pair to coexist that RP-ROLLBACK-002
independently found incompatible

[RP-ORDER-003 and RP-K8S-004 advisory findings also present — abbreviated here]
$ echo $?
1
```

Full, unabbreviated output for both examples above is reproduced
verbatim, byte for byte, whenever you run the same commands yourself —
try `rolloutproof verify --format json examples/unsafe/rollback-after-irreversible-drop | jq .`
for the same result structured for a script instead of a terminal.

## What's implemented

| Family | IDs | What it proves |
|---|---|---|
| RP-DB | 001–007 | Column existence, type compatibility, NOT NULL introduction, renames, expand/contract sequencing, rollback preconditions |
| RP-ROLLBACK | 001–003 | Rollback safety against a real second transition graph, four-state SAFE/CONDITIONALLY_SAFE/UNSAFE/UNKNOWN |
| RP-API | 001–004 | Provider/consumer API contract compatibility (endpoints, request/response fields) over reachable states |
| RP-ORDER | 001–003 | Cross-service dependency version ordering, plus a coarse/advisory migration-phase structural check |
| RP-K8S | 001–004 | Readiness-before-dependency, termination/drain conflicts, coarse/advisory structural checks (visibly marked, never blended into the blocking verdict) |

See `docs/invariants.md` for the full catalog with algorithms and
worked examples, and `docs/FALSE_POSITIVES.md` for exactly what each
family can and cannot prove.

## What's not implemented (yet)

- **No project-config layer** (`docs/architecture.md` §7): version
  scheme, migration-ordering convention, and CI policy for UNKNOWN are
  currently self-contained, honestly-scoped fallbacks in code, not a
  declared project-wide policy.
- **RP-K8S-001** currently only fires by deriving from another
  invariant's already-confirmed finding — it has no independent
  capability to declare two versions "incompatible" absent a
  schema/API conflict RolloutProof can already enumerate itself.
- **RP-K8S-003** reports `UNKNOWN`, not a guess, for a `Recreate`-strategy
  workload with a PreStop hook — the transition graph has no state
  distinctly representing "draining" for that strategy yet.
- **Only Kubernetes `Deployment` + PostgreSQL** are modeled; other
  workload kinds, other rollout controllers (Argo Rollouts canary
  steps, etc.), and other databases are out of scope for now
  (`docs/vision.md` §6).
- **A constrained SQL subset**: `internal/parser/sql` recognizes a fixed
  set of `ALTER TABLE` forms; anything else becomes `OpUnclassified`
  and correctly drives `UNKNOWN`, never a silent guess.

`docs/FALSE_POSITIVES.md` is the complete, current list of precision
boundaries, advisory-vs-high-confidence distinctions, and known model
gaps — read it before trusting a SAFE result further than it actually
extends.

## CI integration

```yaml
- uses: actions/checkout@v4
- uses: SamudralaAjaykumarrr/rolloutproof@main
  with:
    directory: deploy/checkout-rollout
```

No tagged release exists yet (`docs/RELEASING.md`), so `@main` is the
only ref that currently resolves; pin to a `@vX.Y.Z` tag instead once one
is cut. Exits 0/1/2 exactly like the CLI; see `docs/CI.md` for the full
reusable Action (inputs/outputs, `fail-on-unknown`), the generic
exit-code contract any other CI system can use directly, and this
repository's own self-testing workflow (`.github/workflows/ci.yml`).

## JSON and SARIF output

```bash
rolloutproof verify --format json examples/unsafe/drop-column-before-drain | jq '.verdict, .invariants[].id'
rolloutproof verify --format sarif --output report.sarif examples/unsafe/drop-column-before-drain
```

JSON carries a versioned `schemaVersion` field; SARIF 2.1.0 is ready for
`github/codeql-action/upload-sarif` (an UNSAFE finding becomes an
`error`, an *advisory* one a `warning`, UNKNOWN a `note` — see
`docs/CI.md`).

## Design philosophy

- **Never silently SAFE.** Missing evidence produces `UNKNOWN` with the
  exact missing fact named, never a default-to-SAFE fallback
  (`docs/vision.md` §10).
- **Evidence, not scenario names.** Every verdict cites the specific IR
  facts an invariant's precondition actually used
  (`docs/adr/0006`) — nothing is derived from matching a fixture's file
  path or directory name.
- **Deterministic, always.** Identical input produces byte-identical
  output, in every format, on every run (`docs/vision.md` §9) — the
  transition graph is a DAG built and traversed in a fixed order
  specifically so this holds.
- **Coarse and precise findings stay visibly distinct.** An advisory,
  over-inclusive check (`ir.Diagnostic.Advisory`) is still reported in
  full, but never silently vetoes the overall verdict the way a
  high-confidence one does.

## Documentation

| Document | Contents |
|---|---|
| `docs/vision.md` | Problem, target users, thesis, V1 boundaries, non-goals, safety philosophy |
| `docs/architecture.md` | Normalized IR, rollout state model, transition graph, rollback analysis, uncertainty model, package layout |
| `docs/invariants.md` | The full safety invariant catalog (RP-DB, RP-ROLLBACK, RP-API, RP-ORDER, RP-K8S) |
| `docs/failure-model.md` | Every failure mechanism considered, and which ones are modeled, approximated, or excluded |
| `docs/scenario-corpus.md` | 23 worked scenarios (SAFE/UNSAFE/UNKNOWN), each backed by a real fixture under `examples/` (or an explicitly documented model-gap exception) |
| `docs/FALSE_POSITIVES.md` | What's proven, where UNKNOWN fires, what's advisory, known model gaps, and SAFE's assumptions |
| `docs/CI.md` | The reusable GitHub Action, the generic exit-code contract, and JSON/SARIF artifact usage |
| `docs/adr/` | Design decisions and the alternatives rejected for each |
| `docs/review-start.md` | Start here to review this project: a 5-minute runnable path and the primary challenge (find a false SAFE) |
| `docs/ADVERSARIAL_REVIEW.md` | The full adversarial methodology: what was attacked, what was found and fixed, and how to challenge a verdict or report a false SAFE/UNSAFE/UNKNOWN |

## Evaluating the scenario corpus yourself

```bash
go run ./cmd/eval
```

Runs the real pipeline against every fixture under `examples/` and
reports pass/fail against each directory's declared expectation, plus
runtime — a deterministic regression suite, not a demo.

## Reviewing this project / reporting a false verdict

Trying to break RolloutProof, not just use it? Start at
`docs/review-start.md` for a 5-minute runnable path and the primary
challenge (find a rollout that's unsafe under RolloutProof's own modeled
assumptions but verifies SAFE). `docs/ADVERSARIAL_REVIEW.md` has the full
methodology and "How to challenge a verdict" before you conclude a
surprising result is a bug. Report a confirmed false SAFE, false UNSAFE,
or false UNKNOWN with the
[Break RolloutProof issue template](../../issues/new?template=break-rolloutproof.yml).

## Contributing

See `CONTRIBUTING.md` for setup, the required pre-PR checks, and this
project's specific conventions for adding an invariant or a scenario
fixture. This project follows the `CODE_OF_CONDUCT.md`. Report a
suspected vulnerability per `SECURITY.md`, not as a public issue.

## License

Apache License 2.0 — see `LICENSE`.
