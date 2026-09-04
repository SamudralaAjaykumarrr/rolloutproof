# ADR 0005: Tri-state SAFE/UNSAFE/UNKNOWN verdict over boolean safe/unsafe

## Status

Accepted

## Context

RolloutProof's evidence about a rollout is never guaranteed complete —
contract metadata can be missing, a SQL statement can fall outside the
supported subset, a version scheme can be declared unordered
(architecture.md §2.1.1). The engine's output could either force every
evaluation to a boolean (treating incomplete evidence as either safe or
unsafe by convention), or carry a third, explicit state for "cannot be
determined."

## Decision

Every invariant evaluation, and the aggregate rollout verdict, is one of
`Safe`, `Unsafe`, or `Unknown` (`architecture.md` §6), with a strict
dominance order for aggregation: `Unsafe` > `Unknown` > `Safe`. `Unknown`
always carries the specific missing evidence (`EvidenceGap`).

## Alternatives considered

**Boolean, defaulting missing evidence to safe.** Treat "no evidence of a
problem" as SAFE. Rejected as unacceptable: this is precisely the false
SAFE failure mode vision.md §10 names as the worst possible outcome for
this tool — a report with a clean bill of health, backed by nothing, is
more dangerous than no report at all, because it actively displaces
manual scrutiny a team would otherwise apply.

**Boolean, defaulting missing evidence to unsafe.** Treat any gap as a
blocking failure. Considered as the "safe-by-caution" alternative to the
above. Rejected because it is unusable in practice: any project with
incomplete contract metadata (the realistic starting state for essentially
every adopter, per ADR 0004's acknowledged maintenance burden) would see
every rollout blocked, indistinguishable from a rollout with an actual
known defect. This destroys the signal-to-noise ratio a CI gate needs to
remain trusted, and gives teams no way to distinguish "we have a real
problem" from "we haven't finished writing contract metadata yet" —
exactly the distinction vision.md §7's CI-policy discussion depends on
being able to make.

**Tri-state (chosen).** Preserves the distinction that matters: a report
can say "we checked, and it's fine," "we checked, and it's broken," or "we
could not check this — here is exactly why," and a CI pipeline can apply
different policy to the second and third (vision.md §7).

## Consequences

- Every invariant implementation must track and report *why* it could not
  decide, not just that it could not (`EvidenceGap.Field`/`Reason`,
  architecture.md §6) — this is more implementation work per invariant
  than a boolean would require, and is treated as mandatory, not optional
  polish.
- CI integration must expose a policy choice for UNKNOWN (block vs. warn,
  vision.md §7) rather than resolving it internally — RolloutProof commits
  to never making that call silently on a team's behalf.
- The aggregation dominance order (`Unsafe` > `Unknown` > `Safe`) must be
  enforced at exactly one place (architecture.md §6) so that no invariant
  or reporting path can locally "round" an UNKNOWN up to SAFE to reduce
  noise — a temptation this ADR explicitly forecloses.
