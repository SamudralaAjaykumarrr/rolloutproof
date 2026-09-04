package ir

// Counterexample is the structured, deterministic explanation of why an
// invariant returned VerdictUnsafe (docs/architecture.md §11,
// docs/adr/0006). It is built purely from IR facts and the transition
// graph's shortest violating path — never as a hand-written string
// matched against a scenario name.
type Counterexample struct {
	// Path is the shortest sequence of transition events from the
	// rollout's start state to the state in which the invariant is
	// first violated (docs/architecture.md §4.4).
	Path []TransitionEvent

	// ViolatingState is the RolloutState in which the invariant's
	// precondition was found satisfied.
	ViolatingState RolloutState

	// Outcome is a short, invariant-specific description of the concrete
	// failure this path leads to (e.g. "the live version's declared
	// schema access no longer matches the committed schema", or "the
	// consumer receives a response missing a field it requires") — the
	// domain-specific last step a renderer appends after Path. Every
	// invariant that builds a Counterexample must set this; an empty
	// value is a bug (a family-specific claim like a schema mismatch
	// must never be assumed as a generic default — docs/adr/0006).
	Outcome string

	// RecommendedSequence is a human-readable, ordered remediation, when
	// one is derivable from the same evidence. Empty is a valid,
	// honest output when no generic remediation pattern applies
	// (docs/architecture.md §11) — invariants must not fabricate one.
	RecommendedSequence []string
}

// Diagnostic is the complete, self-contained report for one invariant's
// evaluation against one RolloutPlan (docs/vision.md §8). Every UNSAFE or
// UNKNOWN result the engine produces is a Diagnostic; every field a
// human-readable report renders comes from this struct, never from an
// ad hoc string built inside invariant logic (docs/adr/0006).
type Diagnostic struct {
	InvariantID string
	Verdict     Verdict
	Summary     string

	Evidence []Evidence

	// Counterexample is non-nil only when Verdict == VerdictUnsafe.
	Counterexample *Counterexample

	// MissingEvidence is non-empty only when Verdict == VerdictUnknown
	// (docs/architecture.md §6) — an invariant must name exactly what it
	// could not evaluate and why.
	MissingEvidence []EvidenceGap

	// RollbackVerdict classifies rollback safety of the violating state,
	// when evaluated as part of this diagnostic. RollbackUnknown (the
	// zero value) is the correct value when rollback was not evaluated
	// at all, not merely when it was evaluated and found unclear.
	RollbackVerdict RollbackVerdict

	// Advisory marks a coarse, over-inclusive finding that trades
	// precision for recall by design (docs/invariants.md RP-K8S-004's own
	// documented Limitations: "a project may configure it as advisory...
	// precisely because it can fire even when [higher-precision
	// invariants] independently return SAFE with full evidence"). The
	// zero value, false, is correct for every high-confidence invariant —
	// an Advisory UNSAFE diagnostic is still reported in full, but does
	// not by itself veto the overall rollout verdict (Aggregate), keeping
	// coarse and high-confidence findings visibly distinguished rather
	// than silently blended into one blocking signal.
	Advisory bool
}
