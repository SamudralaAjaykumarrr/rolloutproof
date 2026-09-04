# ADR 0006: Structured evidence/counterexample model over hard-coded diagnostics

## Status

Accepted

## Context

An UNSAFE result needs a human-readable explanation. The simplest way to
produce one is to give each invariant a hand-written message template
("unsafe migration: column dropped while old service reads it") filled in
with the specific names involved. A more structured alternative is to
define a shared `Counterexample`/`EvidenceRef` data model
(`architecture.md` §11) that every invariant populates, with rendering
handled by a single, separate reporting layer.

## Decision

Every invariant produces a `Counterexample` struct — cited evidence, the
shortest violating path through the transition graph, and (when derivable)
a recommended sequence (`architecture.md` §11) — and exactly one rendering
function per output format (`internal/report`) turns that struct into
text, JSON, or SARIF. No invariant contains output-format-specific string
formatting.

## Alternatives considered

**Hard-coded, per-invariant diagnostic strings.** Each invariant's
implementation directly constructs its final human-readable message.
Rejected because:

- **Not reusable across output formats.** vision.md §7 commits to text
  now, JSON and SARIF later. A hard-coded string is unusable as a JSON
  field structure or a SARIF result object without re-deriving the
  underlying facts a second time in a second format-specific code path —
  duplicating exactly the logic ADR 0001 exists to avoid duplicating.
- **Cannot support deterministic shortest-counterexample selection.**
  `architecture.md` §4.4 requires selecting the shortest violating path
  when multiple exist. A hard-coded string produced inline during
  traversal has nothing to compare against other candidate strings on; a
  structured `Counterexample` with a `Path` field can be compared and
  ranked by path length before any text is ever generated.
- **Risks looking hard-coded to the scenario, defeating the project's
  core credibility requirement.** The problem statement is explicit that
  the flagship result "must be DERIVED, not hard-coded." A per-invariant
  string template that happens to reproduce the worked example's wording
  is indistinguishable, from the outside, from a template that was
  special-cased for that exact scenario. A shared struct populated purely
  from IR facts and rendered by one generic function is what makes
  "derived, not hard-coded" independently verifiable — a reviewer can
  check that the rendering function contains no scenario-specific logic.

## Consequences

- Every invariant must populate `EvidenceRef`s with enough structured
  detail (`Kind`, `File`, `Locator`, `Description`) for a generic renderer
  to produce a specific, non-generic message — this pushes precision
  requirements onto invariant authors rather than deferring them to
  wherever a message happens to get written.
- Adding a new output format (JSON, SARIF) touches only
  `internal/report`, never `internal/invariant` — verified structurally by
  the package dependency direction in `architecture.md` §10 (no
  `invariant` package may import a format-specific rendering concern).
- The `RecommendedSequence` field is explicitly optional (architecture.md
  §11: "not every invariant does" have a known remediation pattern) —
  invariants must not fabricate a recommendation just to populate the
  field; an empty `RecommendedSequence` is an honest, acceptable output.
