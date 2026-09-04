# RolloutProof

RolloutProof verifies whether a proposed production rollout is safe — not
just whether the Kubernetes manifest and the SQL migration are each
individually valid, but whether the **transition** between the current and
target state is safe, including every mixed-version and partially-applied
intermediate state a real rollout passes through.

**Status: architecture foundation.** No verification engine exists yet.
This repository currently contains the design documents that define what
RolloutProof will do and how, ahead of implementation. See
[`docs/vision.md`](docs/vision.md) for the honest scope and non-goals.

## The core idea

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

No single artifact here is wrong. The *combination and timing* is wrong.
RolloutProof derives this class of defect from a normalized model of
services, schemas, migrations, and rollout mechanics — it does not
hard-code this example or any other.

## Documentation

| Document | Contents |
|---|---|
| [`docs/vision.md`](docs/vision.md) | Problem, target users, thesis, V1 boundaries, non-goals, explainability/determinism requirements, safety philosophy |
| [`docs/architecture.md`](docs/architecture.md) | Normalized IR, rollout state model, transition graph, rollback analysis, uncertainty model, package layout |
| [`docs/invariants.md`](docs/invariants.md) | The safety invariant catalog (RP-DB, RP-K8S, RP-API, RP-ORDER, RP-ROLLBACK) |
| [`docs/failure-model.md`](docs/failure-model.md) | Every failure mechanism considered, and which ones V1 models, approximates, or excludes |
| [`docs/scenario-corpus.md`](docs/scenario-corpus.md) | 23 worked scenarios (SAFE/UNSAFE/UNKNOWN) that will become regression fixtures |
| [`docs/adr/`](docs/adr/) | Design decisions and the alternatives rejected for each |

## V1 scope

V1 verifies exactly one path: a Kubernetes `Deployment` + a PostgreSQL
migration + explicit service/schema dependency metadata, evaluated as a
local, deterministic, offline CLI. See `docs/vision.md` §5–6 for the full
boundary.

## License

TBD.
