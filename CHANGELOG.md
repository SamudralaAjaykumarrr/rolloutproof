# Changelog

All notable changes to RolloutProof are documented in this file.

The format loosely follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning follows the intent of [Semantic Versioning](https://semver.org/);
until the first tagged release, entries are grouped by development phase
instead of a version number.

## [Unreleased]

## [v0.1.0] - 2026-09-04

### Added — cross-layer verification engine

- **RP-DB family (complete, RP-DB-001 through RP-DB-007)**: destructive
  column removal (reads and writes tracked separately), a fixed
  Postgres type-compatibility table (widening/narrowing/incomparable),
  unsafe `NOT NULL` introduction, bare column rename, expand/contract
  migration sequencing, and irreversible-migration-before-rollback
  detection.
- **First-class rollback model** (RP-ROLLBACK-001/002/003): a real,
  separately-evaluated transition graph for a plan's declared
  `RollbackTarget`, with a four-state
  SAFE/CONDITIONALLY_SAFE/UNSAFE/UNKNOWN classification — never treats
  a DOWN-migration file's mere existence as proof of rollback safety.
- **API Contract IR and RP-API family** (RP-API-001 through RP-API-004):
  a provider/consumer contract model (`internal/ir` `APIContract`/
  `Endpoint`/`Shape`/`Field`), evaluated over every reachable rollout
  state rather than a static contract diff.
- **RP-ORDER family** (RP-ORDER-001 through RP-ORDER-003): cross-service
  dependency version-ordering checks, with a self-contained
  dotted-numeric version comparator that honestly reports `UNKNOWN` for
  opaque/unordered version schemes, plus RP-ORDER-003 — the
  migration-timing-side counterpart to RP-K8S-004, closing the one
  catalog ID `docs/invariants.md` documented but that had no
  implementation — an advisory structural check for a destructive
  migration phased during rollout with no declared expand/contract
  sequencing, independent of the workload's own strategy.
- **RP-K8S family's highest-value rules** (RP-K8S-001 through
  RP-K8S-004): readiness-before-dependency, termination/drain conflict
  detection, an advisory structural check for destructive migrations
  under a widened coexistence window, and a "derived transitively"
  coexistence-incompatibility echo — each explicitly scoped to what it
  can actually prove, with `ir.Diagnostic.Advisory` distinguishing
  coarse, over-inclusive findings from high-confidence ones.
- **Machine-readable output**: deterministic JSON (`internal/report.RenderJSON`,
  a versioned `schemaVersion`) and SARIF 2.1.0
  (`internal/report.RenderSARIF`, suitable for GitHub code-scanning
  upload) alongside the original human-readable text report.
- **CLI**: `--format text|json|sarif`, `--output <file>`, and a
  `version` subcommand backed by `internal/version` (overridable via
  `-ldflags -X` for release builds).
- **Reusable GitHub Action** (`action.yml`) another repository can
  reference directly, plus this repository's own self-testing CI
  workflow (`.github/workflows/ci.yml`) that builds the real binary and
  runs it against real SAFE/UNSAFE example fixtures, asserting the
  documented exit codes.
- **Scenario-corpus evaluation harness** (`cmd/eval`): runs the real
  pipeline against every `examples/{safe,unsafe,unknown}/*` fixture and
  reports pass/fail against the directory's declared expectation —
  currently 28 fixtures, 100% passing, covering all 23
  `docs/scenario-corpus.md` scenarios that the current architecture can
  represent (one, SC-SAFE-010, is documented as superseded by more
  precise reachability semantics rather than given a fixture that would
  contradict them — see `docs/scenario-corpus.md`).
- **Benchmarks** (`go test -bench=.` in `internal/graph`,
  `internal/parser/sql`, `internal/invariant`, `internal/verify`) with
  explicit scaling experiments over concurrently-changing workloads and
  in-flight migrations, confirming the transition graph's documented
  `O(3^W x 2^D)` growth is real and measured, not just theoretical.
- **Documentation**: `docs/CI.md` (CI integration, both the reusable
  Action and the generic exit-code contract), `docs/FALSE_POSITIVES.md`
  (precision boundaries — what's proven, where UNKNOWN fires, what's
  advisory, known model gaps, and SAFE's assumptions), `LICENSE`
  (Apache 2.0), `CODE_OF_CONDUCT.md`, `SECURITY.md`, `CONTRIBUTING.md`.

### Fixed

- `internal/graph.Build` had no bound on transition-graph node count; a
  plan with enough `PhaseDuringRollout` migrations could exhaust memory
  or hang. Added `maxGraphNodes`, failing fast with a clear error
  instead (found during adversarial self-review; measured benchmark:
  12 in-flight migrations on a single workload already costs ~146ms and
  ~248MB per `Build()` call).
- `report.RenderText` hardcoded a DB-specific "schema mismatch" as every
  counterexample's final line, which was wrong for non-DB findings
  (API, ordering, K8s). Added `ir.Counterexample.Outcome`, set by every
  invariant to its own domain-accurate description, with a regression
  test asserting every UNSAFE counterexample sets it.
- RP-ROLLBACK-001 and RP-ORDER-002 were built by copying a sibling
  diagnostic and overwriting only `InvariantID`, leaving the *Summary
  text* naming the original ID (RP-ROLLBACK-001's summary always said
  "RP-DB-007:"). Fixed by calling the shared evaluator once per ID
  instead.
- RP-K8S-003 treated any state where an old version was merely live as
  evidence it might be "draining," including the pre-rollout,
  nothing-has-changed-yet state — a false positive for `Recreate`
  specifically, whose model has no state distinctly representing
  "draining" at all. Restricted the approximation to the coexistence
  state (where it is actually defensible, for `RollingUpdate`) and
  changed the verdict to `UNKNOWN` — not a guess either way — for
  `Recreate`/unrecognized strategies.
- RP-DB-001/002 previously fired only by scanning for a locally
  committed `OpDropColumn`, which could not see a column simply absent
  from a graph's base schema (a rollback's starting schema, or a column
  no migration in the plan ever added — `docs/scenario-corpus.md`
  SC-UNSAFE-004/005, previously unreachable). Generalized to the
  existence check the scenario corpus already documents RP-DB-001 as.

### Changed

- README status section rewritten to describe the actual cross-layer
  invariant catalog rather than the earlier DB-only summary.

## Earlier history

The initial architecture-foundation and DB-focused V1 phases (normalized
IR, Kubernetes/SQL/contract-metadata parsers, the reachable-state
transition graph, RP-DB-001/002 as the flagship invariant pair, the
`rolloutproof verify` CLI, and the first end-to-end scenario fixtures)
are summarized in `git log` from the repository's first commit
(`69ee728`) through the start of the "Unreleased" section above; see
individual commit messages for that history's own detail, in the same
style this file continues.
