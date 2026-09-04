package sql

import (
	"strings"
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

func TestParse_AddColumn_NullableNoDefault(t *testing.T) {
	m, err := Parse("001.sql", `ALTER TABLE users ADD COLUMN phone varchar(20);`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(m.Operations))
	}
	op := m.Operations[0]
	if op.Kind != ir.OpAddColumn || op.Table != "users" || op.Column != "phone" || op.NewType != "varchar(20)" {
		t.Fatalf("unexpected op: %+v", op)
	}
	if !op.Nullable {
		t.Fatalf("expected nullable column")
	}
	if op.Destructiveness != ir.NonDestructive {
		t.Fatalf("expected NonDestructive, got %v", op.Destructiveness)
	}
	if op.Reversibility != ir.Reversible {
		t.Fatalf("expected Reversible, got %v", op.Reversibility)
	}
}

func TestParse_AddColumn_NotNullNoDefault_ConditionallyDestructive(t *testing.T) {
	m, err := Parse("002.sql", `ALTER TABLE orders ADD COLUMN status text NOT NULL;`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	op := m.Operations[0]
	if op.Nullable {
		t.Fatalf("expected NOT NULL to produce Nullable=false")
	}
	if op.Destructiveness != ir.ConditionallyDestructive {
		t.Fatalf("expected ConditionallyDestructive, got %v", op.Destructiveness)
	}
}

func TestParse_AddColumn_NotNullWithDefault_NonDestructive(t *testing.T) {
	m, err := Parse("003.sql", `ALTER TABLE orders ADD COLUMN status text NOT NULL DEFAULT 'pending';`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	op := m.Operations[0]
	if op.Default == nil || *op.Default != "'pending'" {
		t.Fatalf("unexpected default: %+v", op.Default)
	}
	if op.Destructiveness != ir.NonDestructive {
		t.Fatalf("expected NonDestructive when a default is present, got %v", op.Destructiveness)
	}
}

func TestParse_DropColumn(t *testing.T) {
	m, err := Parse("004.sql", `ALTER TABLE users DROP COLUMN email;`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	op := m.Operations[0]
	if op.Kind != ir.OpDropColumn || op.Table != "users" || op.Column != "email" {
		t.Fatalf("unexpected op: %+v", op)
	}
	if op.Destructiveness != ir.Destructive || op.Reversibility != ir.Irreversible {
		t.Fatalf("unexpected classification: %+v", op)
	}
}

func TestParse_RenameColumn(t *testing.T) {
	m, err := Parse("005.sql", `ALTER TABLE users RENAME COLUMN email TO email_address;`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	op := m.Operations[0]
	if op.Kind != ir.OpRenameColumn || op.Column != "email" || op.NewColumn != "email_address" {
		t.Fatalf("unexpected op: %+v", op)
	}
}

func TestParse_AlterColumnType(t *testing.T) {
	m, err := Parse("006.sql", `ALTER TABLE users ALTER COLUMN user_id TYPE bigint;`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	op := m.Operations[0]
	if op.Kind != ir.OpAlterColumnType || op.NewType != "bigint" {
		t.Fatalf("unexpected op: %+v", op)
	}
}

func TestParse_AlterColumnType_SetDataTypeWithUsing(t *testing.T) {
	m, err := Parse("007.sql", `ALTER TABLE users ALTER COLUMN user_id SET DATA TYPE bigint USING user_id::bigint;`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	op := m.Operations[0]
	if op.Kind != ir.OpAlterColumnType || op.NewType != "bigint" {
		t.Fatalf("unexpected op: %+v", op)
	}
}

func TestParse_SetNotNull(t *testing.T) {
	m, err := Parse("008.sql", `ALTER TABLE users ALTER COLUMN email SET NOT NULL;`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	op := m.Operations[0]
	if op.Kind != ir.OpSetNotNull {
		t.Fatalf("unexpected op: %+v", op)
	}
}

func TestParse_DropNotNull(t *testing.T) {
	m, err := Parse("009.sql", `ALTER TABLE users ALTER COLUMN email DROP NOT NULL;`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	op := m.Operations[0]
	if op.Kind != ir.OpDropNotNull {
		t.Fatalf("unexpected op: %+v", op)
	}
	if op.Destructiveness != ir.NonDestructive || op.Reversibility != ir.ConditionallyReversible {
		t.Fatalf("unexpected classification: %+v", op)
	}
}

func TestParse_CaseInsensitiveKeywords(t *testing.T) {
	m, err := Parse("010.sql", `alter table Users drop column Email;`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	op := m.Operations[0]
	if op.Kind != ir.OpDropColumn || op.Table != "Users" || op.Column != "Email" {
		t.Fatalf("expected identifier case to be preserved while keywords are case-insensitive: %+v", op)
	}
}

func TestParse_MultipleStatements(t *testing.T) {
	src := `
ALTER TABLE users ADD COLUMN nickname text;
ALTER TABLE users DROP COLUMN legacy_flag;
`
	m, err := Parse("011.sql", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Operations) != 2 {
		t.Fatalf("expected 2 operations, got %d", len(m.Operations))
	}
	if m.Operations[0].Kind != ir.OpAddColumn || m.Operations[1].Kind != ir.OpDropColumn {
		t.Fatalf("unexpected operation order: %+v", m.Operations)
	}
}

func TestParse_NoTrailingSemicolonOnFinalStatement(t *testing.T) {
	m, err := Parse("012.sql", `ALTER TABLE users DROP COLUMN email`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(m.Operations))
	}
}

func TestParse_WhitespaceAndCommentVariants(t *testing.T) {
	src := "-- drop the legacy column\nALTER TABLE   users\n  DROP\tCOLUMN /* trailing */ email ;"
	m, err := Parse("013.sql", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Operations) != 1 || m.Operations[0].Kind != ir.OpDropColumn {
		t.Fatalf("unexpected result: %+v", m.Operations)
	}
}

func TestParse_UnsupportedStatement_BecomesUnclassifiedNotError(t *testing.T) {
	m, err := Parse("014.sql", `CREATE TABLE widgets (id serial primary key);`)
	if err != nil {
		t.Fatalf("unsupported statements must not error: %v", err)
	}
	if len(m.Operations) != 1 || m.Operations[0].Kind != ir.OpUnclassified {
		t.Fatalf("expected a single unclassified op, got %+v", m.Operations)
	}
	if !strings.Contains(m.Operations[0].RawStatement, "CREATE TABLE") {
		t.Fatalf("expected raw statement to be preserved as evidence: %q", m.Operations[0].RawStatement)
	}
}

func TestParse_UnsupportedAlterTableForm_BecomesUnclassified(t *testing.T) {
	m, err := Parse("015.sql", `ALTER TABLE users ADD CONSTRAINT users_email_key UNIQUE (email);`)
	if err != nil {
		t.Fatalf("unsupported ALTER TABLE sub-forms must not error: %v", err)
	}
	if m.Operations[0].Kind != ir.OpUnclassified {
		t.Fatalf("expected unclassified, got %+v", m.Operations[0])
	}
}

func TestParse_MixedSupportedAndUnsupportedStatements(t *testing.T) {
	src := `
ALTER TABLE users DROP COLUMN email;
ALTER TABLE users ADD CONSTRAINT users_name_key UNIQUE (name);
ALTER TABLE users ADD COLUMN nickname text;
`
	m, err := Parse("016.sql", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Operations) != 3 {
		t.Fatalf("expected 3 operations, got %d", len(m.Operations))
	}
	if m.Operations[0].Kind != ir.OpDropColumn || m.Operations[1].Kind != ir.OpUnclassified || m.Operations[2].Kind != ir.OpAddColumn {
		t.Fatalf("unexpected operation kinds: %+v", m.Operations)
	}
}

func TestParse_MalformedSQL_UnterminatedString(t *testing.T) {
	_, err := Parse("017.sql", `ALTER TABLE users ADD COLUMN note text DEFAULT 'unterminated;`)
	if err == nil {
		t.Fatalf("expected an error for an unterminated string literal")
	}
}

func TestParse_MalformedSQL_IncompleteAddColumn(t *testing.T) {
	_, err := Parse("018.sql", `ALTER TABLE users ADD COLUMN;`)
	if err == nil {
		t.Fatalf("expected an error for ADD COLUMN missing a name and type")
	}
	var pe *ParseError
	if !asParseError(err, &pe) {
		t.Fatalf("expected a *ParseError, got %T: %v", err, err)
	}
}

func TestParse_MalformedSQL_RenameMissingTo(t *testing.T) {
	_, err := Parse("019.sql", `ALTER TABLE users RENAME COLUMN email email_address;`)
	if err == nil {
		t.Fatalf("expected an error for RENAME COLUMN missing TO")
	}
}

func TestParse_MalformedSQL_UnexpectedCharacter(t *testing.T) {
	_, err := Parse("020.sql", `ALTER TABLE users ADD COLUMN note text DEFAULT #bad;`)
	if err == nil {
		t.Fatalf("expected an error for an unexpected character")
	}
}

func TestParse_Deterministic(t *testing.T) {
	src := `
ALTER TABLE users DROP COLUMN email;
ALTER TABLE users ADD COLUMN nickname text;
`
	a, err := Parse("021.sql", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, err := Parse("021.sql", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(a.Operations) != len(b.Operations) {
		t.Fatalf("nondeterministic operation count")
	}
	for i := range a.Operations {
		if a.Operations[i] != b.Operations[i] {
			t.Fatalf("nondeterministic parse at operation %d: %+v vs %+v", i, a.Operations[i], b.Operations[i])
		}
	}
}

func TestParse_SourceLineTracking(t *testing.T) {
	src := "ALTER TABLE users\n  DROP COLUMN email;\nALTER TABLE users\n  ADD COLUMN nickname text;"
	m, err := Parse("022.sql", src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Operations[0].SourceLine != 1 {
		t.Fatalf("expected first operation to start at line 1, got %d", m.Operations[0].SourceLine)
	}
	if m.Operations[1].SourceLine != 3 {
		t.Fatalf("expected second operation to start at line 3, got %d", m.Operations[1].SourceLine)
	}
}

func TestParse_QuotedIdentifiersPreserveCase(t *testing.T) {
	m, err := Parse("023.sql", `ALTER TABLE "Users" DROP COLUMN "Email";`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	op := m.Operations[0]
	if op.Table != "Users" || op.Column != "Email" {
		t.Fatalf("unexpected op: %+v", op)
	}
}

func TestParse_SchemaQualifiedTableUsesLastComponent(t *testing.T) {
	m, err := Parse("024.sql", `ALTER TABLE public.users DROP COLUMN email;`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Operations[0].Table != "users" {
		t.Fatalf("expected schema-qualified name to resolve to bare table name, got %q", m.Operations[0].Table)
	}
}

func TestParse_EmptyFileProducesNoOperations(t *testing.T) {
	m, err := Parse("025.sql", "  \n-- just a comment\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Operations) != 0 {
		t.Fatalf("expected no operations, got %+v", m.Operations)
	}
}

func asParseError(err error, target **ParseError) bool {
	pe, ok := err.(*ParseError)
	if !ok {
		return false
	}
	*target = pe
	return true
}
