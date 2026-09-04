# ADR 0004: Explicit dependency metadata over source-code analysis

## Status

Accepted

## Context

Every RP-DB and RP-API invariant needs to know what schema columns and API
endpoints a given service version actually reads, writes, provides, and
consumes. That information could, in principle, be derived by statically
analyzing the service's source code (or by dynamic tracing), or it could
be explicitly declared by the team that owns the service, in a format
RolloutProof defines.

## Decision

Require explicit, versioned dependency/contract metadata
(`internal/parser/contract`, `architecture.md` §8) as the sole source of
`Service.SchemaReads`/`SchemaWrites`/`APIProvides`/`APIConsumes`.
RolloutProof performs no source-code analysis in V1.

## Alternatives considered

**Static source-code analysis.** Parse application source (e.g. Go, Java,
Python) to infer ORM/query calls and their target tables/columns.
Rejected for V1 because:

- **Unbounded scope across languages and frameworks.** A tool that claims
  to infer schema access from source code takes on an open-ended
  commitment to support every ORM, query builder, and raw-SQL calling
  convention in every language a target team uses. Getting this wrong
  silently produces incomplete `SchemaReads`/`Writes` sets, which is
  exactly the false-SAFE failure mode vision.md §10 identifies as the
  single most dangerous outcome for this tool — an incomplete inference
  that is *trusted* is worse than no inference at all.
- **Undermines the tri-state model's honesty.** If RolloutProof silently
  infers and gets it wrong, the failure is invisible — no `EvidenceGap`
  is produced (architecture.md §6), because the engine believes it has
  evidence. Explicit metadata makes absence of evidence *detectable*: a
  service with no contract file, or a contract file that plainly doesn't
  mention a table, produces a visible UNKNOWN rather than an invisible
  wrong SAFE.
- **Dynamic tracing** (recording actual runtime queries) was also
  considered and rejected for V1 on similar grounds, plus a harder
  practical problem: it requires running the service and exercising code
  paths, which conflicts with vision.md §5's requirement that V1 be a
  local, offline, deterministic verifier with no runtime dependency.

**Explicit dependency metadata (chosen).** A human on the owning team
declares, per service version, the facts the invariant catalog needs. This
does place a maintenance burden on teams (see Consequences), but that
burden is the honest price of the guarantee in §10 — it is authored
knowledge, not inferred and possibly-wrong knowledge, and is directly
compatible with reporting UNKNOWN when it's missing or incomplete.

## Consequences

- Teams must author and maintain contract metadata alongside their
  services; this is real, ongoing cost and a real adoption barrier — it is
  not hidden in this ADR, and the vision explicitly does not overclaim
  V1's convenience (vision.md §2, §11).
- RolloutProof's precision is capped by how completely and accurately
  teams maintain this metadata; an incomplete file produces UNKNOWN, never
  a silently wrong SAFE, which is the explicit trade this ADR makes.
- A future version could add *optional*, best-effort source analysis as a
  metadata-authoring aid (suggesting entries for a human to confirm), but
  that is a tooling convenience layered on top of the explicit-metadata
  model, not a replacement for it — the invariant engine's trust boundary
  stays at the declared metadata regardless.
