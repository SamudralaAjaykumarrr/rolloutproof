# ADR 0008: Zero-value safety for verdict and classification enums

## Status

Accepted

## Context

Implementing `internal/ir` surfaced a concrete instance of the false-SAFE
risk `vision.md` §10 and ADR 0005 warn about in the abstract. Go
zero-initializes every value that isn't explicitly set — a struct literal
missing a field, a map lookup miss returning a zero `Verdict`, a bug that
forgets to set `Destructiveness` on a constructed `MigrationOp` — and the
architecture draft's original enum orderings (`Safe Verdict = iota`,
`NonDestructive Destructiveness = iota`, `OpAddColumn OpKind = iota`,
`Reversible Reversibility = iota`) all put a **benign, active claim** at
position zero. That means every one of these bugs would have silently
produced the *safest-looking* possible value, not a visibly-invalid one —
the exact failure mode this whole project exists to catch, just relocated
into RolloutProof's own implementation.

## Decision

Every enum in `internal/ir` that represents a safety verdict or a
migration-operation classification has its zero value reassigned to mean
**"unknown / not yet classified / not yet evaluated"**:

- `Verdict`: zero value is `VerdictUnknown` (was implicitly `Safe`).
- `RollbackVerdict`: zero value is `RollbackUnknown`.
- `Destructiveness`: zero value is `DestructivenessUnknown` (was
  implicitly `NonDestructive`).
- `Reversibility`: zero value is `ReversibilityUnknown` (was implicitly
  `Reversible`).
- `OpKind`: zero value is `OpUnclassified` (was implicitly `OpAddColumn`
  — arguably the single most dangerous possible default, since it's also
  the only non-destructive, always-reversible operation kind).
- `MigrationPhase`: zero value is `PhaseUnknown` (was implicitly
  `PhaseBeforeRollout`). `ir.NewRolloutPlan` additionally rejects
  `PhaseUnknown` outright at construction time (`rolloutplan.go`) rather
  than letting it flow into the graph builder, since an un-anchored
  migration has no defined reachability semantics at all — this is
  stricter than the other enums, which are allowed to exist in the
  `Unknown` state and propagate it, because a migration truly must be
  assigned a phase for the transition graph to be constructible.

This changes the enum orderings documented in `docs/architecture.md`'s
draft type sketches; the implementation is authoritative going forward.

## Alternatives considered

**Keep the architecture draft's orderings and rely on constructors to
always set an explicit value.** Rejected: this only defends against bugs
in code that goes through `ir`'s own constructors (`NewService`,
`NewMigration`, etc.). It does nothing for zero-initialized structs built
directly (common in tests, and not preventable in Go without making every
field unexported with no literal syntax at all, which conflicts with
`internal/ir` needing to be a plain, inspectable data package). A
defense that only works when nobody makes a mistake is not a defense.

**Add a boolean `Evaluated`/`Known` flag alongside each enum instead of
repurposing the zero value.** Considered because it keeps the "obvious"
enum ordering (Safe first) while still flagging non-evaluation. Rejected
as strictly worse: it reintroduces exactly the bug class this ADR fixes
for any code that reads the enum value without also checking the
companion flag — which is just as easy to forget as setting the enum
correctly in the first place, but now requires *two* things to be
right instead of one.

## Consequences

- Every `switch` over one of these enums in later packages
  (`internal/graph`, `internal/invariant`, `internal/report`) must treat
  the zero-value case as a distinct, visible "unknown" branch — Go's
  `default` case in a switch naturally catches an unhandled/zero value,
  which now correctly reads as "insufficient evidence" rather than
  silently falling through to whatever case happens to be listed first.
- `docs/architecture.md`'s type sketches predate this ADR and use the
  pre-fix orderings in a few places; this ADR is the authoritative
  correction, and any future documentation edit to those sketches should
  match `internal/ir`'s actual zero-value ordering, not the reverse.
- Unit tests in `internal/ir` (`TestOpKind_ZeroValueIsUnclassified`,
  `TestDestructiveness_ZeroValueIsUnknown`,
  `TestReversibility_ZeroValueIsUnknown`,
  `TestVerdict_ZeroValueIsUnknown`,
  `TestRollbackVerdict_ZeroValueIsUnknown`) pin this property directly, so
  a future refactor that accidentally reorders one of these `iota` blocks
  fails immediately and loudly rather than being caught only much later
  by a scenario-corpus regression, if at all.
