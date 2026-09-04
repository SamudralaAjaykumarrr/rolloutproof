package ir

import "fmt"

// OpKind identifies the kind of a single migration operation. The zero
// value is OpUnclassified — a MigrationOp that was never assigned a kind
// reads as "we don't know what this does", never as a specific, silently
// assumed-benign operation (see doc.go, "Zero-value safety").
type OpKind int

const (
	OpUnclassified OpKind = iota
	OpAddColumn
	OpDropColumn
	OpRenameColumn
	OpAlterColumnType
	OpSetNotNull
	OpDropNotNull
)

func (k OpKind) String() string {
	switch k {
	case OpAddColumn:
		return "AddColumn"
	case OpDropColumn:
		return "DropColumn"
	case OpRenameColumn:
		return "RenameColumn"
	case OpAlterColumnType:
		return "AlterColumnType"
	case OpSetNotNull:
		return "SetNotNull"
	case OpDropNotNull:
		return "DropNotNull"
	default:
		return "Unclassified"
	}
}

// Destructiveness classifies whether a migration operation can destroy
// data or reject writes that were valid before it ran. The zero value is
// DestructivenessUnknown, not NonDestructive — an operation whose
// destructiveness was never computed must never be silently treated as
// safe (docs/adr/0008).
type Destructiveness int

const (
	DestructivenessUnknown Destructiveness = iota
	NonDestructive
	ConditionallyDestructive
	Destructive
)

func (d Destructiveness) String() string {
	switch d {
	case NonDestructive:
		return "NonDestructive"
	case ConditionallyDestructive:
		return "ConditionallyDestructive"
	case Destructive:
		return "Destructive"
	default:
		return "Unknown"
	}
}

// Reversibility classifies whether a migration operation's effect can be
// undone. The zero value is ReversibilityUnknown, for the same reason
// DestructivenessUnknown is the zero value of Destructiveness.
type Reversibility int

const (
	ReversibilityUnknown Reversibility = iota
	Reversible
	ConditionallyReversible
	Irreversible
)

func (r Reversibility) String() string {
	switch r {
	case Reversible:
		return "Reversible"
	case ConditionallyReversible:
		return "ConditionallyReversible"
	case Irreversible:
		return "Irreversible"
	default:
		return "Unknown"
	}
}

// MigrationOp is one operation within a Migration, already classified by
// the SQL parser (docs/architecture.md §2.4) — the invariant engine and
// transition graph never re-derive Destructiveness/Reversibility
// themselves.
type MigrationOp struct {
	Kind  OpKind
	Table string

	Column    string // AddColumn/DropColumn/RenameColumn(source)/AlterColumnType/SetNotNull/DropNotNull
	NewColumn string // RenameColumn target
	NewType   string // AddColumn type, AlterColumnType target type
	Nullable  bool   // AddColumn only
	Default   *string

	Destructiveness Destructiveness
	Reversibility   Reversibility

	// Evidence-rendering fields: where this operation came from, for
	// diagnostics (docs/architecture.md §11). SourceLine is 1-indexed;
	// 0 means unknown.
	RawStatement string
	SourceLine   int
}

// TargetColumn returns the ColumnRef this operation primarily concerns
// (the source column for a rename).
func (op MigrationOp) TargetColumn() ColumnRef {
	return ColumnRef{Table: op.Table, Column: op.Column}
}

func (op MigrationOp) validate() error {
	if op.Kind == OpUnclassified {
		return nil // unclassified ops are deliberately under-specified
	}
	if op.Table == "" {
		return fmt.Errorf("ir: migration operation %s: table must not be empty", op.Kind)
	}
	switch op.Kind {
	case OpAddColumn:
		if op.Column == "" {
			return fmt.Errorf("ir: AddColumn on %q: column must not be empty", op.Table)
		}
	case OpDropColumn, OpAlterColumnType, OpSetNotNull, OpDropNotNull:
		if op.Column == "" {
			return fmt.Errorf("ir: %s on %q: column must not be empty", op.Kind, op.Table)
		}
	case OpRenameColumn:
		if op.Column == "" || op.NewColumn == "" {
			return fmt.Errorf("ir: RenameColumn on %q: both source and target column required", op.Table)
		}
		if op.Column == op.NewColumn {
			return fmt.Errorf("ir: RenameColumn on %q: source and target column identical (%q)", op.Table, op.Column)
		}
	}
	return nil
}

// Migration is an ordered sequence of operations parsed from one file.
// Cross-file ordering between migrations is a property of the project's
// naming convention, applied by whatever assembles a []Migration into a
// timeline (docs/architecture.md §2.4) — Migration itself only orders its
// own Operations.
type Migration struct {
	ID         string // typically the source file name
	Operations []MigrationOp
}

// NewMigration validates and constructs a Migration.
func NewMigration(id string, ops []MigrationOp) (Migration, error) {
	if id == "" {
		return Migration{}, fmt.Errorf("ir: migration id must not be empty")
	}
	for i, op := range ops {
		if err := op.validate(); err != nil {
			return Migration{}, fmt.Errorf("ir: migration %s: operation %d: %w", id, i, err)
		}
	}
	out := make([]MigrationOp, len(ops))
	copy(out, ops)
	return Migration{ID: id, Operations: out}, nil
}

// HasUnclassifiedOperations reports whether any operation in this
// migration could not be classified by the parser. A migration timeline
// containing such an operation cannot have its post-migration schema
// state fully derived (see Schema.Apply, ErrUnclassifiedOperation).
func (m Migration) HasUnclassifiedOperations() bool {
	for _, op := range m.Operations {
		if op.Kind == OpUnclassified {
			return true
		}
	}
	return false
}
