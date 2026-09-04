# Review RolloutProof

This is the fast path into an external review. For the full adversarial
methodology (what was already attacked, how, and what was found), read
`docs/ADVERSARIAL_REVIEW.md` next — this page is deliberately short.

## 5-minute path

```bash
git clone https://github.com/SamudralaAjaykumarrr/rolloutproof
cd rolloutproof

# Build and run the three verdicts against real fixtures:
go build -o rolloutproof ./cmd/rolloutproof
./rolloutproof verify examples/safe/additive-column             # exit 0, SAFE
./rolloutproof verify examples/unsafe/drop-column-before-drain  # exit 1, UNSAFE
./rolloutproof verify examples/unknown/missing-service-metadata # exit 2, UNKNOWN

# Inspect a counterexample structured for scripting:
./rolloutproof verify --format json examples/unsafe/drop-column-before-drain | jq .

# Run the full regression corpus (28 fixtures, real engine, no mocks):
go run ./cmd/eval

# Run the full test/fuzz/property gate this project holds itself to:
gofmt -l .
go vet ./...
go test ./...
go test -race ./...
go test -fuzz=FuzzAdversarialRollout -fuzztime=60s ./internal/invariant/...
```

If any of the above fails, disagrees with itself across runs, or produces
output that doesn't match what's documented, that is already a finding —
see "How to report" below.

## What I want challenged

- **False SAFE** — a rollout RolloutProof reports SAFE that actually has
  a reachable, unsafe intermediate state. The single most serious
  category; see `docs/vision.md` §10's non-negotiable rule.
- **Missing reachable states** — a state the transition graph should
  generate (per `docs/architecture.md` §3-4) but doesn't, or an
  impossible state it generates that it shouldn't.
- **Rollback reasoning** — a case where `RP-ROLLBACK-*`'s SAFE /
  CONDITIONALLY_SAFE / UNSAFE / UNKNOWN classification doesn't match what
  actually happens if you roll back.
- **PostgreSQL semantic gaps** — a migration form `internal/parser/sql`
  either misclassifies or should recognize but treats as
  `OpUnclassified`.
- **Kubernetes assumptions** — a `RollingUpdate`/`Recreate` mechanic,
  readiness/termination interaction, or coexistence window RolloutProof
  models incorrectly.
- **API compatibility assumptions** — a provider/consumer contract
  change `RP-API-*` should flag (or shouldn't) that it gets wrong.
- **State-space explosion** — an input shape that makes
  `internal/graph.Build` blow up past what `maxGraphNodes` is meant to
  cap, or that hangs instead of failing fast.

## Primary challenge

**Find a rollout that is unsafe under RolloutProof's own modeled
assumptions but which RolloutProof reports SAFE.**

"Under RolloutProof's own modeled assumptions" matters: a plan built on
contract metadata that doesn't match the real running system is a
metadata-authoring problem, not a verifier bug (`docs/adr/0004`). The
challenge is to construct a plan where the *declared* facts entail a
hazard the engine's own reachability analysis and invariant catalog
should catch, and doesn't.

## How to report

Open an issue with the
["Break RolloutProof"](../../../issues/new?template=break-rolloutproof.yml)
template. `docs/ADVERSARIAL_REVIEW.md`'s "How to challenge a verdict" and
"Minimal reproduction format" sections describe exactly what makes a
report actionable versus a documented scope boundary.
