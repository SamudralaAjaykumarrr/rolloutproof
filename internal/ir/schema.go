package ir

import (
	"fmt"
	"sort"
)

// Column is a single table column. Type is a raw, un-normalized type
// name as it appears in the schema/migration (e.g. "varchar(255)",
// "integer") — V1 does no cross-dialect normalization beyond what the SQL
// parser itself already does when classifying migration operations.
type Column struct {
	Name     string
	Type     string
	Nullable bool
	Default  *string // nil means no default
}

func (c Column) equal(o Column) bool {
	if c.Name != o.Name || c.Type != o.Type || c.Nullable != o.Nullable {
		return false
	}
	if (c.Default == nil) != (o.Default == nil) {
		return false
	}
	if c.Default != nil && *c.Default != *o.Default {
		return false
	}
	return true
}

// Table is an immutable-by-convention named collection of columns.
// Construct via NewTable; the zero value is not a valid Table.
type Table struct {
	name    string
	columns map[string]Column
}

func (t Table) Name() string { return t.name }

// Columns returns the table's columns sorted by name, so iteration order
// never depends on Go's randomized map ordering (docs/vision.md §9).
func (t Table) Columns() []Column {
	out := make([]Column, 0, len(t.columns))
	for _, c := range t.columns {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Column looks up a column by name.
func (t Table) Column(name string) (Column, bool) {
	c, ok := t.columns[name]
	return c, ok
}

func (t Table) HasColumn(name string) bool {
	_, ok := t.columns[name]
	return ok
}

func (t Table) withColumn(c Column) Table {
	next := make(map[string]Column, len(t.columns)+1)
	for k, v := range t.columns {
		next[k] = v
	}
	next[c.Name] = c
	return Table{name: t.name, columns: next}
}

func (t Table) withoutColumn(name string) Table {
	next := make(map[string]Column, len(t.columns))
	for k, v := range t.columns {
		if k == name {
			continue
		}
		next[k] = v
	}
	return Table{name: t.name, columns: next}
}

// NewTable validates and constructs a Table. Duplicate column names
// (case-sensitive, matching Postgres unquoted-identifier folding being
// the SQL parser's concern, not this constructor's) are a construction
// error, not a silent overwrite — a duplicate here means the caller
// (typically a parser) built a nonsensical Schema.
func NewTable(name string, columns []Column) (Table, error) {
	if name == "" {
		return Table{}, fmt.Errorf("ir: table name must not be empty")
	}
	m := make(map[string]Column, len(columns))
	for _, c := range columns {
		if c.Name == "" {
			return Table{}, fmt.Errorf("ir: table %q: column with empty name", name)
		}
		if _, dup := m[c.Name]; dup {
			return Table{}, fmt.Errorf("ir: table %q: duplicate column %q", name, c.Name)
		}
		m[c.Name] = c
	}
	return Table{name: name, columns: m}, nil
}

// Schema is an immutable-by-convention snapshot of table definitions.
// Every migration operation produces a new Schema value via Apply; no
// Schema is ever mutated in place (docs/architecture.md §2.3).
type Schema struct {
	tables map[string]Table
}

// Tables returns the schema's tables sorted by name.
func (s Schema) Tables() []Table {
	out := make([]Table, 0, len(s.tables))
	for _, t := range s.tables {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// Table looks up a table by name.
func (s Schema) Table(name string) (Table, bool) {
	t, ok := s.tables[name]
	return t, ok
}

// HasColumn reports whether the given table and column both exist in
// this schema snapshot.
func (s Schema) HasColumn(ref ColumnRef) bool {
	t, ok := s.tables[ref.Table]
	if !ok {
		return false
	}
	return t.HasColumn(ref.Column)
}

func (s Schema) withTable(t Table) Schema {
	next := make(map[string]Table, len(s.tables)+1)
	for k, v := range s.tables {
		next[k] = v
	}
	next[t.name] = t
	return Schema{tables: next}
}

// NewSchema validates and constructs a Schema from a set of tables.
func NewSchema(tables []Table) (Schema, error) {
	m := make(map[string]Table, len(tables))
	for _, t := range tables {
		if t.name == "" {
			return Schema{}, fmt.Errorf("ir: schema: table with empty name")
		}
		if _, dup := m[t.name]; dup {
			return Schema{}, fmt.Errorf("ir: schema: duplicate table %q", t.name)
		}
		m[t.name] = t
	}
	return Schema{tables: m}, nil
}

// Apply returns the Schema that results from applying op to s. It is a
// pure function: s is never modified. Applying an operation that
// references a nonexistent table or column, or that would conflict with
// existing structure (e.g. adding a column that already exists), is a
// construction-time error — schema application is meant to run over
// syntactically-parsed, internally-consistent migrations, so this error
// signals either a malformed migration or a bug in the caller's ordering,
// not a runtime user-facing verification outcome.
func (s Schema) Apply(op MigrationOp) (Schema, error) {
	switch op.Kind {
	case OpAddColumn:
		t, ok := s.tables[op.Table]
		if !ok {
			t = Table{name: op.Table, columns: map[string]Column{}}
		}
		if t.HasColumn(op.Column) {
			return Schema{}, fmt.Errorf("ir: cannot add column %s.%s: already exists", op.Table, op.Column)
		}
		col := Column{Name: op.Column, Type: op.NewType, Nullable: op.Nullable, Default: op.Default}
		return s.withTable(t.withColumn(col)), nil

	case OpDropColumn:
		t, ok := s.tables[op.Table]
		if !ok || !t.HasColumn(op.Column) {
			return Schema{}, fmt.Errorf("ir: cannot drop column %s.%s: does not exist", op.Table, op.Column)
		}
		return s.withTable(t.withoutColumn(op.Column)), nil

	case OpRenameColumn:
		t, ok := s.tables[op.Table]
		if !ok {
			return Schema{}, fmt.Errorf("ir: cannot rename column on unknown table %q", op.Table)
		}
		col, ok := t.Column(op.Column)
		if !ok {
			return Schema{}, fmt.Errorf("ir: cannot rename column %s.%s: does not exist", op.Table, op.Column)
		}
		if t.HasColumn(op.NewColumn) {
			return Schema{}, fmt.Errorf("ir: cannot rename %s.%s to %s: target already exists", op.Table, op.Column, op.NewColumn)
		}
		col.Name = op.NewColumn
		return s.withTable(t.withoutColumn(op.Column).withColumn(col)), nil

	case OpAlterColumnType:
		t, col, err := s.mustColumn(op.Table, op.Column)
		if err != nil {
			return Schema{}, err
		}
		col.Type = op.NewType
		return s.withTable(t.withColumn(col)), nil

	case OpSetNotNull:
		t, col, err := s.mustColumn(op.Table, op.Column)
		if err != nil {
			return Schema{}, err
		}
		col.Nullable = false
		return s.withTable(t.withColumn(col)), nil

	case OpDropNotNull:
		t, col, err := s.mustColumn(op.Table, op.Column)
		if err != nil {
			return Schema{}, err
		}
		col.Nullable = true
		return s.withTable(t.withColumn(col)), nil

	case OpUnclassified:
		// An unclassified operation's effect on the schema is, by
		// definition, not known. Returning s unchanged would silently
		// understate the schema's real drift, which risks a false SAFE
		// downstream; returning an error here instead forces every
		// caller building a schema timeline to handle "this migration
		// contains an operation we cannot model" explicitly (surfaced as
		// UNKNOWN by internal/graph, never silently skipped).
		return Schema{}, fmt.Errorf("ir: cannot apply unclassified migration operation on %s.%s: %w", op.Table, op.Column, ErrUnclassifiedOperation)

	default:
		return Schema{}, fmt.Errorf("ir: unknown migration operation kind %v", op.Kind)
	}
}

func (s Schema) mustColumn(table, column string) (Table, Column, error) {
	t, ok := s.tables[table]
	if !ok {
		return Table{}, Column{}, fmt.Errorf("ir: unknown table %q", table)
	}
	col, ok := t.Column(column)
	if !ok {
		return Table{}, Column{}, fmt.Errorf("ir: unknown column %s.%s", table, column)
	}
	return t, col, nil
}

// ErrUnclassifiedOperation is returned by Schema.Apply when asked to
// apply an OpUnclassified operation. Callers building a schema timeline
// across a migration should treat this as "the resulting schema state is
// unknown from this point forward", not as a fatal error to propagate
// verbatim to a human without context.
var ErrUnclassifiedOperation = fmt.Errorf("unclassified migration operation")
