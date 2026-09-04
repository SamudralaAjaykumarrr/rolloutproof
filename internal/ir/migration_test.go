package ir

import "testing"

func TestOpKind_ZeroValueIsUnclassified(t *testing.T) {
	var k OpKind
	if k != OpUnclassified {
		t.Fatalf("expected zero value of OpKind to be OpUnclassified, got %v", k)
	}
}

func TestDestructiveness_ZeroValueIsUnknown(t *testing.T) {
	var d Destructiveness
	if d != DestructivenessUnknown {
		t.Fatalf("expected zero value of Destructiveness to be DestructivenessUnknown, got %v", d)
	}
	if d == NonDestructive {
		t.Fatalf("zero-value Destructiveness must never equal NonDestructive")
	}
}

func TestReversibility_ZeroValueIsUnknown(t *testing.T) {
	var r Reversibility
	if r != ReversibilityUnknown {
		t.Fatalf("expected zero value of Reversibility to be ReversibilityUnknown, got %v", r)
	}
}

func TestNewMigration_RejectsEmptyID(t *testing.T) {
	if _, err := NewMigration("", nil); err == nil {
		t.Fatalf("expected error for empty migration id")
	}
}

func TestNewMigration_ValidatesOperations(t *testing.T) {
	_, err := NewMigration("001_bad.sql", []MigrationOp{
		{Kind: OpAddColumn, Table: "users"}, // missing Column
	})
	if err == nil {
		t.Fatalf("expected error for AddColumn with empty column")
	}
}

func TestNewMigration_RejectsRenameToSameName(t *testing.T) {
	_, err := NewMigration("002.sql", []MigrationOp{
		{Kind: OpRenameColumn, Table: "users", Column: "email", NewColumn: "email"},
	})
	if err == nil {
		t.Fatalf("expected error for rename with identical source/target")
	}
}

func TestNewMigration_AllowsUnclassifiedWithoutTableOrColumn(t *testing.T) {
	m, err := NewMigration("003.sql", []MigrationOp{
		{Kind: OpUnclassified, RawStatement: "CREATE TRIGGER ..."},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !m.HasUnclassifiedOperations() {
		t.Fatalf("expected HasUnclassifiedOperations to be true")
	}
}

func TestMigration_HasUnclassifiedOperations_FalseWhenFullyClassified(t *testing.T) {
	m, err := NewMigration("004.sql", []MigrationOp{
		{Kind: OpAddColumn, Table: "users", Column: "email", NewType: "text", Nullable: true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.HasUnclassifiedOperations() {
		t.Fatalf("expected HasUnclassifiedOperations to be false")
	}
}

func TestMigrationOp_TargetColumn(t *testing.T) {
	op := MigrationOp{Kind: OpDropColumn, Table: "users", Column: "email"}
	if got := op.TargetColumn(); got != (ColumnRef{Table: "users", Column: "email"}) {
		t.Fatalf("unexpected TargetColumn: %+v", got)
	}
}
