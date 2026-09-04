package ir

// Verdict is RolloutProof's tri-state safety result (docs/vision.md §10,
// docs/architecture.md §6). The zero value is VerdictUnknown, not
// VerdictSafe — a Verdict that was never explicitly set must read as "not
// evaluated", never as a positive safety claim (see doc.go, "Zero-value
// safety"; docs/adr/0005; docs/adr/0008).
type Verdict int

const (
	VerdictUnknown Verdict = iota
	VerdictSafe
	VerdictUnsafe
)

func (v Verdict) String() string {
	switch v {
	case VerdictSafe:
		return "SAFE"
	case VerdictUnsafe:
		return "UNSAFE"
	default:
		return "UNKNOWN"
	}
}

// Dominance gives Verdict its aggregation order: Unsafe dominates
// Unknown dominates Safe (docs/architecture.md §6). Higher wins.
func (v Verdict) dominance() int {
	switch v {
	case VerdictUnsafe:
		return 2
	case VerdictUnknown:
		return 1
	default:
		return 0
	}
}

// Combine returns the dominant of two verdicts under the aggregation rule
// Unsafe > Unknown > Safe. Used to fold a set of per-invariant verdicts
// into one overall rollout verdict without ever letting an Unknown or
// Unsafe result be silently outvoted by a Safe one.
func Combine(a, b Verdict) Verdict {
	if a.dominance() >= b.dominance() {
		return a
	}
	return b
}

// RollbackVerdict is a four-state classification of rollback safety
// (distinct from Verdict because "conditionally safe" is a meaningful,
// reportable rollback-specific outcome — docs/architecture.md §5). The
// zero value is RollbackUnknown for the same zero-value-safety reason as
// Verdict.
type RollbackVerdict int

const (
	RollbackUnknown RollbackVerdict = iota
	RollbackSafe
	RollbackConditionallySafe
	RollbackUnsafe
)

func (r RollbackVerdict) String() string {
	switch r {
	case RollbackSafe:
		return "SAFE"
	case RollbackConditionallySafe:
		return "CONDITIONALLY_SAFE"
	case RollbackUnsafe:
		return "UNSAFE"
	default:
		return "UNKNOWN"
	}
}

func (r RollbackVerdict) dominance() int {
	switch r {
	case RollbackUnsafe:
		return 3
	case RollbackUnknown:
		return 2
	case RollbackConditionallySafe:
		return 1
	default:
		return 0
	}
}

// CombineRollback folds two RollbackVerdicts under the dominance order
// Unsafe > Unknown > ConditionallySafe > Safe. Unknown outranks
// ConditionallySafe deliberately: "we don't know" must never be
// collapsed into a specific, milder classification just because another
// fact was more encouraging (docs/vision.md §10).
func CombineRollback(a, b RollbackVerdict) RollbackVerdict {
	if a.dominance() >= b.dominance() {
		return a
	}
	return b
}

// EvidenceGap names one specific piece of evidence that was missing when
// an invariant tried to reach a verdict. Always populated when a
// Diagnostic's Verdict is VerdictUnknown (docs/architecture.md §6) — an
// UNKNOWN result with no EvidenceGap is a bug, not a valid output.
type EvidenceGap struct {
	Field  string // e.g. "Service.SchemaReads for api@v1"
	Reason string // e.g. "no contract metadata file found for service api at version v1"
}
