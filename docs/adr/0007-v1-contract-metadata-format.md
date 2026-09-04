# ADR 0007: V1 service/schema contract metadata format

## Status

Accepted

## Context

ADR 0004 decided that `Service.SchemaReads`/`SchemaWrites`/`DependsOn`
(architecture.md §2.1) are sourced from explicit, human-authored metadata,
never inferred from source code. That decision left the concrete file
format undefined — `architecture.md` §8 called this out as "schema TBD in
implementation, not architecture." Parsers cannot be built against an
undefined format, so this ADR resolves it before `internal/parser/contract`
is implemented.

## Decision

One YAML file per **(service, version)** pair, discovered by directory
scan (default: a `contracts/` directory relative to the verification
target, configurable). Each file is fully self-contained — no cross-file
includes or inheritance in V1, so that a single file is always sufficient
to answer "what does this service version do."

### Shape

```yaml
apiVersion: rolloutproof.dev/v1alpha1
kind: ServiceContract

service: api
version: v1

schema:
  reads:
    - users.email
    - users.id
  writes:
    - users.name

dependencies:
  - payments
  - service: notifications
    minCompatibleVersion: v2
```

### Field rules

- **`apiVersion`** (required, string). Must be exactly
  `rolloutproof.dev/v1alpha1` in V1. Any other value — including a
  plausible-looking future one like `v1alpha2` — is a **parse error**
  (tool/input failure), not UNKNOWN: an unrecognized schema version means
  RolloutProof cannot safely interpret the file's semantics at all, which
  is a different failure class from "the file is valid but a specific
  fact is missing" (see §Uncertainty vs. parse errors below).
- **`kind`** (required, string, must equal `ServiceContract`). Reserved so
  a future second metadata kind (e.g. `APIContract`, deferred per
  architecture.md §8 until RP-API implementation) can share a directory
  without ambiguity.
- **`service`** (required, non-empty string). The service name, matched
  against `Workload.ServiceName` (architecture.md §2.2).
- **`version`** (required, non-empty string). Matched against
  `Workload.Version` under the project's declared version scheme
  (architecture.md §2.1.1).
- **`schema.reads` / `schema.writes`** (optional, list of strings). Each
  entry is `table.column` — split on the **first** `.` only, since
  unquoted Postgres identifiers never contain `.`. A value with zero or
  more than one `.` is a validation error. Absence of the whole `schema`
  block, or of `reads`/`writes` individually, means "no declared reads/
  writes" as a **closed-world fact about this file** (the file exists and
  chose not to declare any) — this is what SC-SAFE-005 in
  `scenario-corpus.md` depends on, and is different from the file being
  entirely absent (SC-UNKNOWN-001), which the loader — not the parser —
  detects and reports as `EvidenceGap`.
- **`dependencies`** (optional, list). Each entry is either a bare string
  (service name, unconstrained — `MinCompatibleVersion` empty means "no
  version constraint declared," which drives RP-ORDER invariants to
  UNKNOWN rather than assuming compatibility) or a mapping
  `{service, minCompatibleVersion}`. Both forms coexist because the
  common case (a same-team, always-compatible internal dependency) should
  not require boilerplate, while the case that actually drives RP-ORDER
  needs the explicit field.
- **Unknown top-level or nested fields are a validation error** (strict,
  not ignored). A typo'd field name (`shema:` instead of `schema:`) that
  is silently ignored would produce an empty `SchemaReads`/`Writes` and
  masquerade as SC-SAFE-005's legitimate closed-world empty case — exactly
  the false-SAFE risk ADR 0005 exists to prevent. Forward compatibility is
  handled by bumping `apiVersion`, not by tolerating unrecognized fields
  under the current one.

### Duplicate handling

A repeated `table.column` entry within `reads` or within `writes` is
deduplicated (semantically it declares the same fact twice) and is not a
validation error. A `table.column` appearing in **both** `reads` and
`writes` is valid and expected (most write paths also read).

### Validation rules summary

| Condition | Outcome |
|---|---|
| Missing/wrong `apiVersion` or `kind` | Parse error (tool failure) |
| Missing `service` or `version` | Parse error |
| Malformed `table.column` entry (no `.` or multiple `.`) | Parse error |
| Unknown field anywhere in the document | Parse error |
| Duplicate entry within one list | Deduplicated, no error |
| `schema` block entirely absent | Valid — empty reads/writes (closed-world) |
| File for a live `(service, version)` entirely absent | Not a parser concern — the loader reports `EvidenceGap` (architecture.md §6) when the invariant engine looks up a `Service` and finds none |
| `dependencies` references a service with no contract file of its own | Valid at parse time; the *referenced* service's own facts are independently subject to the same absent-file → `EvidenceGap` rule when evaluated |

### Forward compatibility policy

V1 supports exactly `apiVersion: rolloutproof.dev/v1alpha1`. A future
version that needs new fields (e.g. `api.provides`/`api.consumes` for
RP-API invariants, deferred per architecture.md §8) ships as
`rolloutproof.dev/v1beta1` or `v1`, with the parser package supporting
whichever versions are implemented and rejecting the rest by name — never
by silently ignoring fields it doesn't recognize. This mirrors Kubernetes'
own `apiVersion`/`kind` convention deliberately, since the target audience
already carries that mental model.

## Alternatives considered

**Embed contract metadata inside the Kubernetes manifest as annotations.**
Rejected: annotations are string-typed and untyped-nested-structure-hostile
in YAML, would couple contract authorship to whoever edits the Deployment
(often platform/infra, not the team with the schema-access knowledge), and
would make `internal/parser/k8s` respond to a second, unrelated concern
(architecture.md §10's package-boundary discipline forbids a parser
serving two purposes).

**Single project-wide file listing every service/version.** Rejected:
does not scale past a handful of services, produces large diffs on every
version bump unrelated to the change being reviewed, and encourages one
team to accidentally edit another team's declared facts. Per-service,
per-version files keep ownership boundaries aligned with file boundaries.

**Free-form key-value / no schema versioning.** Rejected: gives up the
ability to distinguish "old parser, new unrecognized format" from
"malformed file," which is exactly the ambiguity `apiVersion` and strict
unknown-field rejection resolve.

## Consequences

- `internal/parser/contract` implements exactly one `apiVersion` in V1 and
  is expected to grow a version-dispatch table, not a growing pile of
  optional/tolerated fields, as the format evolves.
- Strict unknown-field rejection means a typo in a contract file is a
  build/CI-visible parse error, not a silent gap — this is a deliberate
  usability/safety trade-off in favor of safety, consistent with ADR 0005.
- The `(service, version)`-per-file convention means N services × M
  live versions produce N×M small files; this is accepted as the honest
  cost of ADR 0004's explicit-metadata decision, not optimized away in V1.
