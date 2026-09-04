package ir

// EvidenceKind names the category of fact an Evidence entry cites.
type EvidenceKind int

const (
	EvidenceUnknown EvidenceKind = iota
	EvidenceMigrationOp
	EvidenceSchemaRead
	EvidenceSchemaWrite
	EvidenceRolloutStrategy
	EvidenceServiceContract
)

func (k EvidenceKind) String() string {
	switch k {
	case EvidenceMigrationOp:
		return "migration_op"
	case EvidenceSchemaRead:
		return "schema_read"
	case EvidenceSchemaWrite:
		return "schema_write"
	case EvidenceRolloutStrategy:
		return "rollout_strategy"
	case EvidenceServiceContract:
		return "service_contract"
	default:
		return "unknown"
	}
}

// Evidence is one specific, machine-readable fact cited by a Diagnostic
// (docs/architecture.md §11's EvidenceRef, docs/adr/0006). A Diagnostic's
// Evidence list must contain only facts an invariant's precondition check
// actually used — never "all input files" (docs/vision.md §8).
type Evidence struct {
	Kind EvidenceKind

	// Artifact is the source file this fact came from (e.g.
	// "migrations/017_drop_email.sql", "contracts/api-v1.yaml",
	// "deployment/api.yaml").
	Artifact string

	// Locator is the semantic location within Artifact: a SQL
	// statement's ordinal, a YAML path, or similar — whatever the
	// owning parser can supply. Empty if not applicable.
	Locator string

	// Line is the 1-indexed source line, when known. 0 means unknown.
	Line int

	// Description is a short, human-readable statement of the fact
	// itself (e.g. "migration drops users.email",
	// "api@v1 declares SchemaReads: users.email").
	Description string
}
