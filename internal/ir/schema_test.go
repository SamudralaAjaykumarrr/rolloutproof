package ir

import "testing"

func mustTable(t *testing.T, name string, cols []Column) Table {
	t.Helper()
	tab, err := NewTable(name, cols)
	if err != nil {
		t.Fatalf("NewTable(%q): unexpected error: %v", name, err)
	}
	return tab
}

func mustSchema(t *testing.T, tables ...Table) Schema {
	t.Helper()
	s, err := NewSchema(tables)
	if err != nil {
		t.Fatalf("NewSchema: unexpected error: %v", err)
	}
	return s
}

func TestNewTable_RejectsDuplicateColumn(t *testing.T) {
	_, err := NewTable("users", []Column{
		{Name: "email", Type: "text"},
		{Name: "email", Type: "text"},
	})
	if err == nil {
		t.Fatalf("expected error for duplicate column")
	}
}

func TestNewTable_RejectsEmptyName(t *testing.T) {
	if _, err := NewTable("", nil); err == nil {
		t.Fatalf("expected error for empty table name")
	}
}

func TestNewSchema_RejectsDuplicateTable(t *testing.T) {
	users := mustTable(t, "users", nil)
	_, err := NewSchema([]Table{users, users})
	if err == nil {
		t.Fatalf("expected error for duplicate table")
	}
}

func TestSchema_ColumnsSortedDeterministically(t *testing.T) {
	tab := mustTable(t, "users", []Column{
		{Name: "z_col", Type: "text"},
		{Name: "a_col", Type: "text"},
		{Name: "m_col", Type: "text"},
	})
	cols := tab.Columns()
	if len(cols) != 3 || cols[0].Name != "a_col" || cols[1].Name != "m_col" || cols[2].Name != "z_col" {
		t.Fatalf("expected sorted columns, got %+v", cols)
	}
}

func TestSchema_ApplyAddColumn(t *testing.T) {
	base := mustSchema(t, mustTable(t, "users", []Column{{Name: "id", Type: "integer"}}))
	next, err := base.Apply(MigrationOp{Kind: OpAddColumn, Table: "users", Column: "email", NewType: "text", Nullable: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !next.HasColumn(ColumnRef{Table: "users", Column: "email"}) {
		t.Fatalf("expected users.email to exist after AddColumn")
	}
	if base.HasColumn(ColumnRef{Table: "users", Column: "email"}) {
		t.Fatalf("Apply must not mutate the receiver: base schema was modified")
	}
}

func TestSchema_ApplyAddColumn_RejectsExisting(t *testing.T) {
	base := mustSchema(t, mustTable(t, "users", []Column{{Name: "email", Type: "text"}}))
	if _, err := base.Apply(MigrationOp{Kind: OpAddColumn, Table: "users", Column: "email", NewType: "text"}); err == nil {
		t.Fatalf("expected error when adding a column that already exists")
	}
}

func TestSchema_ApplyDropColumn(t *testing.T) {
	base := mustSchema(t, mustTable(t, "users", []Column{
		{Name: "id", Type: "integer"},
		{Name: "email", Type: "text"},
	}))
	next, err := base.Apply(MigrationOp{Kind: OpDropColumn, Table: "users", Column: "email"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next.HasColumn(ColumnRef{Table: "users", Column: "email"}) {
		t.Fatalf("expected users.email to be gone after DropColumn")
	}
	if !base.HasColumn(ColumnRef{Table: "users", Column: "email"}) {
		t.Fatalf("Apply must not mutate the receiver")
	}
}

func TestSchema_ApplyDropColumn_RejectsMissing(t *testing.T) {
	base := mustSchema(t, mustTable(t, "users", []Column{{Name: "id", Type: "integer"}}))
	if _, err := base.Apply(MigrationOp{Kind: OpDropColumn, Table: "users", Column: "ghost"}); err == nil {
		t.Fatalf("expected error when dropping a nonexistent column")
	}
}

func TestSchema_ApplyRenameColumn(t *testing.T) {
	base := mustSchema(t, mustTable(t, "users", []Column{{Name: "email", Type: "text", Nullable: true}}))
	next, err := base.Apply(MigrationOp{Kind: OpRenameColumn, Table: "users", Column: "email", NewColumn: "email_address"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next.HasColumn(ColumnRef{Table: "users", Column: "email"}) {
		t.Fatalf("old column name should no longer exist")
	}
	if !next.HasColumn(ColumnRef{Table: "users", Column: "email_address"}) {
		t.Fatalf("new column name should exist")
	}
	col, _ := next.Table("users")
	c, _ := col.Column("email_address")
	if !c.Nullable {
		t.Fatalf("rename should preserve column attributes")
	}
}

func TestSchema_ApplyRenameColumn_RejectsCollisionWithExistingTarget(t *testing.T) {
	base := mustSchema(t, mustTable(t, "users", []Column{
		{Name: "email", Type: "text"},
		{Name: "email_address", Type: "text"},
	}))
	if _, err := base.Apply(MigrationOp{Kind: OpRenameColumn, Table: "users", Column: "email", NewColumn: "email_address"}); err == nil {
		t.Fatalf("expected error when rename target already exists")
	}
}

func TestSchema_ApplySetNotNullAndDropNotNull(t *testing.T) {
	base := mustSchema(t, mustTable(t, "users", []Column{{Name: "email", Type: "text", Nullable: true}}))
	notNull, err := base.Apply(MigrationOp{Kind: OpSetNotNull, Table: "users", Column: "email"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tab, _ := notNull.Table("users")
	col, _ := tab.Column("email")
	if col.Nullable {
		t.Fatalf("expected email to be NOT NULL after SetNotNull")
	}

	nullable, err := notNull.Apply(MigrationOp{Kind: OpDropNotNull, Table: "users", Column: "email"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tab2, _ := nullable.Table("users")
	col2, _ := tab2.Column("email")
	if !col2.Nullable {
		t.Fatalf("expected email to be nullable again after DropNotNull")
	}
}

func TestSchema_ApplyAlterColumnType(t *testing.T) {
	base := mustSchema(t, mustTable(t, "users", []Column{{Name: "user_id", Type: "integer"}}))
	next, err := base.Apply(MigrationOp{Kind: OpAlterColumnType, Table: "users", Column: "user_id", NewType: "bigint"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tab, _ := next.Table("users")
	col, _ := tab.Column("user_id")
	if col.Type != "bigint" {
		t.Fatalf("expected type bigint, got %q", col.Type)
	}
}

func TestSchema_ApplyUnclassified_ReturnsError(t *testing.T) {
	base := mustSchema(t, mustTable(t, "users", []Column{{Name: "id", Type: "integer"}}))
	if _, err := base.Apply(MigrationOp{Kind: OpUnclassified, Table: "users", Column: "id"}); err == nil {
		t.Fatalf("expected error for unclassified operation, not a silent no-op")
	}
}

func TestSchema_ApplyOnUnknownTable_ReturnsError(t *testing.T) {
	base := mustSchema(t)
	if _, err := base.Apply(MigrationOp{Kind: OpDropColumn, Table: "ghost", Column: "id"}); err == nil {
		t.Fatalf("expected error when operating on an unknown table")
	}
}

func TestColumn_SemanticEquality(t *testing.T) {
	def1 := "0"
	def2 := "0"
	a := Column{Name: "n", Type: "int", Nullable: false, Default: &def1}
	b := Column{Name: "n", Type: "int", Nullable: false, Default: &def2}
	if !a.equal(b) {
		t.Fatalf("expected columns with equal-valued but distinct Default pointers to be semantically equal")
	}
	c := Column{Name: "n", Type: "int", Nullable: false, Default: nil}
	if a.equal(c) {
		t.Fatalf("expected columns to differ when one has a default and the other doesn't")
	}
}
