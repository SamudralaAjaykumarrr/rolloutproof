# RolloutProof — Vision

## Status

Architecture foundation. No verification engine exists yet. Nothing in this
document describes shipped behavior; it describes what RolloutProof is being
built to do and the boundaries it must respect while doing it.

## 1. The problem

Production rollouts fail even when every artifact involved is individually
valid. A Kubernetes `Deployment` manifest can be schema-valid. A SQL migration
can run cleanly end to end. A service can pass its own test suite. None of
that guarantees that *deploying the migration and the deployment together, in
the order and timing Kubernetes actually uses, is safe*.

The failure mode RolloutProof targets is specifically the **transition**
between two valid states, not the states themselves:

- `api:v1` is a valid, working service. It reads `users.email`.
- `017_drop_email.sql` is a valid, reversible-looking migration. It drops
  `users.email`.
- A `RollingUpdate` deployment strategy is valid Kubernetes configuration. It
  permits `api:v1` and `api:v2` pods to serve traffic simultaneously during
  the rollout.

Each of these three artifacts, reviewed in isolation, passes every linter and
every schema validator available today. Combined, they produce a window in
which a live `api:v1` pod receives traffic, queries a column that no longer
exists, and fails in production. No single artifact was wrong. The
*combination and ordering* was wrong.

Existing tooling does not look for this class of defect, because existing
tooling evaluates artifacts one at a time:

- YAML/JSON schema validators check that a manifest is structurally valid.
- `kubeval`, `kubeconform`, and admission controllers check a manifest
  against the Kubernetes API schema, not against other services' behavior.
- SQL migration linters (`squawk`, `sqlfluff`, migration-tool dry-runs) check
  that a migration is syntactically valid and sometimes that it avoids known
  dangerous patterns (e.g., a bare `ADD COLUMN ... NOT NULL` without a
  default). They do not know what code reads or writes the column.
- API contract testing (Pact, OpenAPI diff tools) checks whether two
  contract *versions* are compatible. It does not know when each version is
  actually live in production relative to a schema migration or a rollout
  strategy.
- CI type checks and unit tests validate a single version of a single
  service against itself.

None of these tools models the fact that a Kubernetes rolling update
produces a window of **mixed-version coexistence**, that a migration can
commit *before*, *during*, or *after* that window, and that the safety of
the whole rollout depends on the interaction between rollout mechanics,
schema state, and service version at every point in that window.

## 2. Target users

- **Platform / infrastructure engineers** who own the deployment pipeline
  and are accountable for rollout safety across many services they did not
  write.
- **SRE / production engineering teams** who investigate rollout-caused
  incidents after the fact and want a way to catch the same class of defect
  before it reaches production.
- **Backend engineers** shipping a schema migration alongside a service
  change, who want to know whether the combination is safe *before* merging,
  not after paging someone.

RolloutProof is not aimed at end users of the services being deployed, and it
is not a general-purpose database migration tool, ORM, or deployment
orchestrator. It consumes artifacts that already exist in a rollout's
pipeline (manifests, migrations, contract metadata) and evaluates them
together.

## 3. Why existing linting is insufficient

The unifying gap across every tool listed in §1 is **scope**: each one
reasons about a single artifact, a single version, or a single point in
time. RolloutProof's reason to exist is that it reasons about:

1. **Multiple artifacts together** (deployment manifest + migration +
   service contract), not each in isolation.
2. **Time as a first-class dimension** — what is true *during* a rollout,
   not only what is true before and after it.
3. **Coexistence, not just succession** — Kubernetes rolling updates,
   canaries, and blue/green strategies all produce periods where two
   versions of a service are simultaneously live. Most tooling implicitly
   assumes a single active version.

A linter that only reads one file at a time cannot, even in principle, catch
the flagship failure in §1, because the unsafe fact ("api:v1 requires
users.email") and the destructive change ("migration drops users.email")
live in different files owned by different teams, and the exposure window
("both versions can be live at once") is a property of the *deployment
strategy*, not of either artifact.

## 4. Core technical thesis

> **A rollout is not safe because its start state and end state are each
> individually valid. A rollout is safe only if every state reachable during
> the transition — including mixed-version and partially-applied states —
> satisfies the system's safety invariants.**

RolloutProof operationalizes this by:

1. Parsing heterogeneous rollout artifacts into a **normalized intermediate
   representation (IR)** that removes format-specific noise and expresses
   only what is safety-relevant (see `architecture.md`).
2. Deriving the set of **reachable intermediate states** a rollout can pass
   through, given the deployment strategy and migration timing, under
   explicit and documented reachability assumptions (see
   `architecture.md` §Rollout State Model).
3. Evaluating a catalog of **safety invariants** against every reachable
   state, each invariant defined independently of any specific scenario (see
   `invariants.md`).
4. Reporting **SAFE**, **UNSAFE**, or **UNKNOWN**, where UNSAFE is always
   backed by a concrete, minimal counterexample derived from the IR — never
   a hard-coded message — and UNKNOWN is reported whenever evidence is
   insufficient to decide (see `failure-model.md`, §Uncertainty below).

The flagship scenario in §1 must be a *derived* consequence of this
pipeline — evidence in, invariant evaluation, counterexample out — not a
special case detected by name. If RolloutProof cannot derive it generally,
it does not count as solved.

## 5. V1 boundaries

V1 proves exactly one path, chosen because it is the highest-value,
highest-frequency real-world failure class:

```
Kubernetes Deployment manifest
        +
PostgreSQL migration (ordered SQL operations)
        +
explicit service/schema dependency metadata (RolloutProof's own format)
        ↓
   normalized IR
        ↓
reachable mixed-version states
        ↓
   invariant evaluation
        ↓
SAFE / UNSAFE / UNKNOWN + counterexample + recommended sequence
```

V1 is a **local, deterministic, offline verifier**. It is invoked against a
set of files (or a directory) and produces a report. It does not run inside
a cluster, does not watch live rollouts, and does not require network
access.

## 6. Explicit non-goals (V1 and near-term)

RolloutProof does **not**, in V1 or in the near-term roadmap that follows
it:

- Perform static or dynamic analysis of application source code to infer
  what columns, tables, or endpoints a service actually touches. Service
  behavior is declared through explicit, versioned dependency metadata that
  the owning team maintains (see `architecture.md` §Parser Design). This is
  a deliberate scope boundary, not a temporary limitation to be silently
  removed later — see §9.
- Support databases other than PostgreSQL. The migration model is built
  around PostgreSQL's DDL semantics (`ALTER TABLE`, constraint behavior,
  lock semantics). Other databases are a possible future IR backend, not a
  V1 concern.
- Support deployment platforms other than Kubernetes `Deployment` /
  `RollingUpdate`. Other Kubernetes rollout mechanisms (`StatefulSet`
  ordered updates, `Job`, custom operators/CRDs like Argo Rollouts canaries)
  and other orchestrators (Nomad, ECS) are explicitly out of scope for V1.
- Actually execute, block, or gate a rollout. RolloutProof is a verifier
  that produces a report; wiring that report into a CI gate or admission
  controller is an integration concern layered on top (see §7), not part of
  the verification engine itself.
- Guarantee runtime correctness of application logic, business logic
  correctness, performance, or cost. RolloutProof verifies a specific,
  named class of transition-safety defects — it is not a general
  correctness or QA tool.
- Model multi-region, multi-cluster, or cross-datacenter rollout topology.
  V1 models a single rollout of a single service (or a small explicit
  dependency graph of services) against a single database.
- Detect defects that require information no artifact in scope declares —
  see §8 (Uncertainty) for how RolloutProof behaves when this happens,
  rather than silently ignoring the gap.

## 7. Eventual CI/CD integration

V1 is a CLI that exits non-zero on UNSAFE and prints a human-readable
report. Planned, not yet built:

- **JSON output** — a stable, versioned schema for the SAFE/UNSAFE/UNKNOWN
  result and counterexample, so other tools can consume it.
- **SARIF output** — for surfacing findings as code-scanning annotations in
  GitHub/GitLab.
- **GitHub Actions integration** — a thin action wrapper around the CLI,
  with PR-comment reporting of counterexamples.
- **CI policy for UNKNOWN** — because UNKNOWN is not SAFE, CI integration
  must let a team choose whether UNKNOWN blocks a merge (strict) or only
  warns (permissive), on a per-invariant or per-pipeline basis. RolloutProof
  itself never silently resolves UNKNOWN to SAFE to avoid blocking a
  pipeline — resolving that ambiguity is a policy decision for the
  integrating team, and it must be an explicit, visible configuration
  choice with a safe default (block).

None of this is required for the architecture foundation to be sound: the
core engine's output contract (SAFE/UNSAFE/UNKNOWN + structured evidence)
is designed so that CLI text, JSON, and SARIF are all projections of the
same underlying result, not three separate implementations.

## 8. Explainability requirements

Every UNSAFE result must be traceable, deterministically, to:

1. The specific invariant violated (a stable, versioned ID — e.g.
   `RP-DB-004`).
2. The specific evidence (file, line/statement, artifact) that satisfies
   each precondition of that invariant.
3. A concrete counterexample: an ordered sequence of states and the event
   at which the invariant is first violated (see `architecture.md`
   §Counterexample Model).
4. A recommended safe sequence, when one is derivable from the same
   evidence (e.g., "drain old replicas before applying the destructive
   migration").

"Unsafe migration" with no further detail is not an acceptable output at any
verbosity level. If RolloutProof cannot produce evidence and a
counterexample, it has not established UNSAFE — see §9.

## 9. Determinism requirements

- **Same input, same output.** Given the same artifact set, RolloutProof
  must always produce the same result, the same counterexample (when
  multiple exist, always the same *selected* one — see
  `architecture.md` §Counterexample Selection for the deterministic
  ordering rule), and the same diagnostic text.
- **Irrelevant reordering is a no-op.** Reordering YAML map keys, reordering
  independent Kubernetes manifests within a multi-document file, or
  reordering migration files that do not depend on each other must not
  change the result.
- **No time-of-day, environment, or network dependence.** V1 is a pure
  function of its input artifacts. It does not call out to a live cluster
  or a live database to decide safety.
- **No randomness in traversal or reporting**, including in state-space
  exploration and counterexample search (see `architecture.md` §Transition
  Graph, deterministic traversal).

Determinism is what makes RolloutProof's output usable as a CI gate and as a
regression-testable artifact (see the testing strategy referenced from
`architecture.md`); a verifier whose answer can change between runs on
identical input is not trustworthy as either.

## 10. Safety philosophy

RolloutProof is conservative by design:

- It only asserts **UNSAFE** when it has direct evidence connecting a
  destructive or incompatible change to a reachable state in which that
  change causes a failure.
- It never asserts **SAFE** by default. SAFE is a positive conclusion that
  requires the relevant invariants to have been evaluated against all
  reachable states, with sufficient evidence, and found not to be violated.
- Where evidence is insufficient to evaluate an invariant, RolloutProof
  reports **UNKNOWN**, never SAFE. This is the single most important
  correctness property of the tool: a false SAFE is worse than a false
  UNSAFE, because a false SAFE actively misleads a team into shipping a
  dangerous rollout with a clean report in hand. A false or overly frequent
  UNKNOWN is merely annoying. The engine is tuned to fail toward annoyance,
  never toward false confidence.

## 11. Unsupported / uncertain-state behavior

RolloutProof does not claim to prove safety properties that its static
evidence cannot establish. Concretely:

- If a service's dependency metadata does not declare whether it reads a
  given column, RolloutProof does not assume it does not — it reports
  UNKNOWN for any invariant whose evaluation depends on that fact, and
  names the missing evidence explicitly.
- If a migration's destructiveness cannot be classified from its SQL AST
  (e.g., a raw, unparseable statement, or a statement type not yet modeled),
  RolloutProof reports UNKNOWN for that operation rather than guessing.
- If a Kubernetes rollout uses a strategy, field, or controller RolloutProof
  does not yet model, it reports UNKNOWN for the invariants that depend on
  that field rather than silently skipping them or assuming a default that
  may not hold.
- RolloutProof does not model runtime behavior it has no static evidence
  for: actual traffic patterns, actual request timing during a rollout, or
  actual pod scheduling latency. Its state model is a conservative
  *reachability* model (see `architecture.md`), not a simulation of what
  will actually happen on a given day.

Every such UNKNOWN carries the specific missing evidence in its diagnostic,
so a team can either supply the missing metadata or consciously accept the
risk (see §7 on CI policy for UNKNOWN).

## 12. What "done" looks like for the architecture foundation

This document, together with `docs/architecture.md`, `docs/invariants.md`,
`docs/failure-model.md`, `docs/scenario-corpus.md`, and `docs/adr/`, must be
sufficient for a platform/SRE engineer unfamiliar with the project to
understand: the exact problem being solved, why it is not solved by
existing tools, what RolloutProof's V1 will and will not attempt, and how to
judge whether a given implementation faithfully realizes this design. No
implementation work should begin until that bar is met.
