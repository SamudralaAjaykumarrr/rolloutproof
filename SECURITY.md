# Security Policy

## Scope

RolloutProof is a local, offline, read-only CLI (`docs/vision.md` §5):
it reads Kubernetes/SQL/YAML artifacts from disk and writes a report to
stdout or a file. It does not open network sockets, connect to a live
cluster or database, or execute anything it parses. The realistic
security surface is therefore:

- **Parser robustness**: malformed or adversarial input (a crafted
  `deployment.yaml`, migration `.sql`, or `contracts/*.yaml`) causing a
  panic, an infinite loop, or unbounded resource consumption, rather
  than a clean parse error or `UNKNOWN` verdict.
- **The reusable GitHub Action** (`action.yml`): script-injection risk
  from untrusted input reaching a `run:` step unquoted, or overly broad
  permissions.
- **Supply chain**: the Go module dependency tree (`go.sum`) and the
  release build process.

It is explicitly **not** in scope to treat a SAFE/UNSAFE/UNKNOWN
verdict itself as a security boundary — RolloutProof is a correctness
tool, not an access-control or sandboxing mechanism (see
`docs/FALSE_POSITIVES.md` for what a verdict does and does not prove).

## Reporting a Vulnerability

Please report suspected vulnerabilities privately, not via a public
issue:

1. Preferred: open a
   [GitHub Security Advisory](https://github.com/SamudralaAjaykumarrr/rolloutproof/security/advisories/new)
   for this repository. This is private between you and the
   maintainers until a fix is available.
2. If you cannot use GitHub Security Advisories, open a regular issue
   asking a maintainer to open a private channel — do not include
   exploit details in that issue itself.

Please include:

- The affected version or commit.
- A minimal reproduction (a crafted input file, or the exact command
  that triggers the problem) — per `CONTRIBUTING.md`'s general
  bug-report guidance, but for a security report, share it only through
  the private channel above, not a public issue.
- What you observed (a panic, a hang, unbounded memory growth) versus
  what you expected (a clean parse error, a bounded runtime).

## What to expect

- Acknowledgement of a report within a reasonable time.
- A best-effort assessment of severity and, where applicable, a
  regression test added alongside the fix (this project's standing
  practice for every defect found during its own adversarial review —
  see `CHANGELOG.md`).
- Credit in the fix's commit message/changelog entry, unless you
  request otherwise.

## Known, already-mitigated classes

For transparency, these adversarial input classes have already been
tested against and are covered by regression tests as of this writing:

- Malformed/binary/empty YAML and SQL input (parse error, never a
  panic).
- A migration crafted to make `internal/graph.Build` construct an
  unbounded number of transition-graph nodes (`maxGraphNodes`,
  `internal/graph`'s own doc comment) — fails fast with a clear error
  instead of exhausting memory or hanging.
- `internal/parser/sql` is fuzz-tested (`go test -fuzz=FuzzParse
  ./internal/parser/sql`) with no crashes found across millions of
  generated inputs to date.

This list is not exhaustive and will grow as further review finds more.
