# Using RolloutProof in CI

RolloutProof is a local, deterministic, offline CLI (`docs/vision.md`
§5): it needs no network access, no live cluster, and no live database.
Any CI system that can run a Go binary can gate a deploy on it. This
document covers the GitHub Actions path in detail, plus the generic
exit-code contract every other CI system can use directly.

## The exit-code contract (any CI system)

`rolloutproof verify <directory>` exits:

| Code | Meaning |
|---|---|
| `0` | **SAFE** — every implemented invariant evaluated, none violated |
| `1` | **UNSAFE** — at least one invariant is violated |
| `2` | **UNKNOWN** — no invariant is violated, but at least one could not be fully evaluated for lack of evidence |
| `>2` | Tool/input failure — a malformed file, a missing required file, or anything else that prevented verification from running at all |

This holds for every `--format` (`text`, `json`, `sarif`) — the format
only changes the *shape* of the report, never the verdict or the exit
code (`cmd/rolloutproof`'s own package doc comment).

A minimal, CI-system-agnostic gate:

```bash
go install github.com/SamudralaAjaykumarrr/rolloutproof/cmd/rolloutproof@latest
rolloutproof verify ./deploy/checkout-rollout
```

Treat exit code `2` (UNKNOWN) as a warning, not necessarily a hard
failure — see "Deciding what to do with UNKNOWN" below.

## GitHub Actions: the reusable Action

This repository's own `action.yml` is a composite action any other
repository can reference directly, without vendoring or building
anything by hand. Pin to a tagged release (`docs/RELEASING.md`) rather
than `@main`, so a change on `main` can't silently alter what your CI
gate runs:

```yaml
name: Verify rollout safety
on:
  pull_request:
    paths:
      - 'deploy/**'

jobs:
  rolloutproof:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: SamudralaAjaykumarrr/rolloutproof@v0.1.0
        with:
          directory: deploy/checkout-rollout
```

That's the entire integration. The action builds RolloutProof from
source on the runner (Go toolchain only — `go-version-file` reads this
repo's own `go.mod`) and runs `verify` against `inputs.directory`.

### Inputs

| Input | Required | Default | Meaning |
|---|---|---|---|
| `directory` | yes | — | Path to the rollout plan directory (the `internal/parser/rolloutplan` convention — see `examples/`) |
| `format` | no | `text` | `text`, `json`, or `sarif` |
| `output` | no | *(empty)* | Write the report to this file instead of the step log |
| `fail-on-unknown` | no | `false` | When `true`, an UNKNOWN verdict also fails the step |

### Outputs

| Output | Meaning |
|---|---|
| `verdict` | `SAFE`, `UNSAFE`, `UNKNOWN`, or `TOOL_FAILURE` |
| `exit-code` | The literal exit code `rolloutproof verify` produced |

### Behavior by verdict

- **SAFE** (exit `0`) — the step succeeds. Nothing else happens.
- **UNSAFE** (exit `1`) — the step fails with `::error::`. The rendered
  report (evidence, counterexample, recommended sequence) is in the step
  log, or in the `--output` file if you set one.
- **UNKNOWN** (exit `2`) — the step **succeeds** by default. RolloutProof
  is telling you it doesn't have enough evidence to prove either SAFE or
  UNSAFE (`docs/vision.md` §10) — that is a real, honest answer, not
  automatically a defect in your rollout. Set `fail-on-unknown: true` if
  your team wants CI to block on missing contract metadata too (a
  stricter, "prove it or block it" policy some teams may prefer once
  contract-metadata coverage is established).

### Reading `verdict`/`exit-code` in a later step

```yaml
      - uses: SamudralaAjaykumarrr/rolloutproof@v0.1.0
        id: rp
        with:
          directory: deploy/checkout-rollout
        continue-on-error: true   # inspect the result yourself instead of failing here
      - name: Comment on PR if UNSAFE
        if: steps.rp.outputs.verdict == 'UNSAFE'
        run: echo "would post a PR comment here"
```

## Producing a JSON or SARIF artifact

```yaml
      - uses: SamudralaAjaykumarrr/rolloutproof@v0.1.0
        with:
          directory: deploy/checkout-rollout
          format: sarif
          output: rolloutproof.sarif
        continue-on-error: true   # upload the artifact even on UNSAFE

      - uses: actions/upload-artifact@v4
        with:
          name: rolloutproof-report
          path: rolloutproof.sarif

      - uses: github/codeql-action/upload-sarif@v3
        with:
          sarif_file: rolloutproof.sarif
```

A SARIF UNSAFE finding becomes an `error`-level result (or `warning` for
an *advisory* invariant like RP-K8S-004 — see
`docs/FALSE_POSITIVES.md` — never conflated with a high-confidence
one); UNKNOWN becomes a `note`. SAFE diagnostics produce no SARIF result
at all. Uploading to GitHub code scanning surfaces UNSAFE findings as
repository security alerts, with the violating file/line as the
location when the invariant cited one.

The JSON format (`internal/report.RenderJSON`) is the stable
machine-readable shape for anything else — a Slack bot, a custom PR
comment, a policy engine:

```bash
rolloutproof verify --format json deploy/checkout-rollout > report.json
jq '.verdict, .invariants[] | select(.verdict != "SAFE") | .id' report.json
```

Every JSON report carries a `schemaVersion` field
(`rolloutproof.dev/report/v1` today) — check it before parsing
programmatically, the same discipline `docs/adr/0007` applies to
contract-metadata files, so a future breaking change to the shape is a
visible version bump, not a silent drift your parser trips over.

## This repository's own CI

`.github/workflows/ci.yml` is not a demo file — it is the workflow that
actually gates every push/PR to this repo:

- **`test`**: `gofmt -l .`, `go vet ./...`, `go build ./...`,
  `go test ./...`, `go test -race ./...`, the `cmd/eval` scenario-corpus
  regression suite, and `git diff --check`.
- **`self-verify`**: builds the real `rolloutproof` binary and runs it
  against a real SAFE example and a real UNSAFE example from `examples/`,
  asserting the exact exit codes RolloutProof itself documents — then
  produces JSON and SARIF artifacts from the UNSAFE example and uploads
  the SARIF to code scanning.

Both jobs exercise the actual CLI end to end; neither prints
pre-recorded or scripted output.

## Deciding what to do with UNKNOWN

UNKNOWN means "RolloutProof does not have enough declared evidence to
answer" — usually a missing or incomplete `contracts/*.yaml` file for a
live service version (`docs/scenario-corpus.md` SC-UNKNOWN-001). Two
reasonable CI policies:

1. **Warn, don't block** (`fail-on-unknown: false`, the default) — good
   while a team is still adding contract metadata across services;
   UNKNOWN findings are visible in the log/artifact but don't stop a
   deploy.
2. **Block** (`fail-on-unknown: true`) — good once contract-metadata
   coverage is established and a team wants "prove it's safe, or it
   doesn't ship" as policy. This is the stricter, and more
   conservative, choice — see `docs/vision.md` §10 for why UNKNOWN is
   never silently treated as SAFE in the first place.

Either way, the missing-evidence detail (`internal/ir.EvidenceGap` — a
specific field and reason, never a generic "insufficient data") is in
the report, so the fix is always "declare the missing fact," not
guesswork.
