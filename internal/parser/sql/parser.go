package sql

import (
	"fmt"
	"strings"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// ParseError reports a statement that resembles one of V1's supported
// ALTER TABLE forms but is missing a token that form requires (e.g.
// "ALTER TABLE users ADD COLUMN" with no column name/type). This is
// distinct from an unsupported-but-well-formed statement, which never
// produces an error (see package doc) — a ParseError means the author
// most likely made a mistake worth surfacing directly, not a statement
// RolloutProof simply doesn't model yet.
type ParseError struct {
	File    string
	Line    int
	Message string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("sql: %s:%d: %s", e.File, e.Line, e.Message)
}

// Parse parses the full contents of one migration file into an
// ir.Migration. id is used as the resulting Migration's ID (conventionally
// the file name). Parse returns an error only for a genuinely malformed
// lexical structure or a statement that matches a supported form's
// keyword shape but is missing required tokens — never for a merely
// unsupported statement, which instead becomes an ir.OpUnclassified
// operation (see package doc).
func Parse(id, src string) (ir.Migration, error) {
	toks, err := lex(src)
	if err != nil {
		le := err.(*lexError)
		return ir.Migration{}, &ParseError{File: id, Line: le.Line, Message: le.Message}
	}

	stmts := splitStatements(toks)
	ops := make([]ir.MigrationOp, 0, len(stmts))
	for _, stmt := range stmts {
		op, err := parseStatement(id, src, stmt)
		if err != nil {
			return ir.Migration{}, err
		}
		ops = append(ops, op)
	}

	m, err := ir.NewMigration(id, ops)
	if err != nil {
		return ir.Migration{}, fmt.Errorf("sql: %s: %w", id, err)
	}
	return m, nil
}

// splitStatements groups tokens into one slice per top-level
// semicolon-delimited statement, dropping the EOF sentinel and empty
// statements produced by trailing/doubled semicolons. Semicolons inside
// string literals were already consumed as part of a single tokString
// token by the lexer, so this split is always statement-boundary-safe
// for anything the lexer successfully tokenized.
func splitStatements(toks []token) [][]token {
	var stmts [][]token
	var cur []token
	for _, t := range toks {
		if t.kind == tokEOF {
			break
		}
		if t.kind == tokPunct && t.text == ";" {
			if len(cur) > 0 {
				stmts = append(stmts, cur)
			}
			cur = nil
			continue
		}
		cur = append(cur, t)
	}
	if len(cur) > 0 {
		stmts = append(stmts, cur)
	}
	return stmts
}

// stmtParser walks one statement's tokens with lookahead-1 semantics.
type stmtParser struct {
	src  string
	file string
	toks []token
	pos  int
}

func (p *stmtParser) peek() token {
	if p.pos >= len(p.toks) {
		return token{kind: tokEOF}
	}
	return p.toks[p.pos]
}

func (p *stmtParser) atEnd() bool { return p.pos >= len(p.toks) }

func (p *stmtParser) advance() token {
	t := p.peek()
	if p.pos < len(p.toks) {
		p.pos++
	}
	return t
}

// acceptKeyword consumes the next token if it is the given identifier
// (case-insensitive), returning whether it matched.
func (p *stmtParser) acceptKeyword(kw string) bool {
	if p.peek().is(kw) {
		p.advance()
		return true
	}
	return false
}

// name reads one possibly schema-qualified identifier ("users" or
// "public.users"), returning it joined by "." and whether one was
// present. Quoted identifiers are supported and preserve exact case.
//
// Known limitation: RolloutProof does not resolve Postgres' search_path;
// a migration referencing "public.users" and contract metadata
// referencing "users" are treated as distinct tables. Projects should use
// one convention consistently.
func (p *stmtParser) name() (string, bool) {
	first := p.peek()
	if first.kind != tokIdent && first.kind != tokQuotedIdent {
		return "", false
	}
	p.advance()
	parts := []string{first.text}
	for p.peek().kind == tokPunct && p.peek().text == "." {
		p.advance()
		next := p.peek()
		if next.kind != tokIdent && next.kind != tokQuotedIdent {
			return "", false
		}
		p.advance()
		parts = append(parts, next.text)
	}
	return strings.Join(parts, "."), true
}

// lastQualifiedPart returns the final component of a possibly
// schema-qualified name, used as the bare column/table identifier callers
// compare against contract metadata by default.
func lastQualifiedPart(qualified string) string {
	parts := strings.Split(qualified, ".")
	return parts[len(parts)-1]
}

var typeTerminators = map[string]bool{"not": true, "default": true, "using": true}

// typeName consumes tokens describing a type ("varchar(255)",
// "numeric(10,2)", "timestamp with time zone") until a terminator keyword
// or end of statement, and returns the exact source substring spanned,
// trimmed of surrounding whitespace.
func (p *stmtParser) typeName() (string, bool) {
	start := p.pos
	if p.atEnd() {
		return "", false
	}
	for !p.atEnd() {
		t := p.peek()
		if t.kind == tokIdent && typeTerminators[strings.ToLower(t.text)] {
			break
		}
		p.advance()
	}
	if p.pos == start {
		return "", false
	}
	first := p.toks[start]
	last := p.toks[p.pos-1]
	return strings.TrimSpace(p.src[first.start:last.end]), true
}

// restAsText consumes all remaining tokens in the statement and returns
// the exact source substring they span, used for DEFAULT/USING
// expressions V1 stores verbatim without interpreting.
func (p *stmtParser) restAsText() (string, bool) {
	if p.atEnd() {
		return "", false
	}
	start := p.pos
	last := p.toks[len(p.toks)-1]
	p.pos = len(p.toks)
	return strings.TrimSpace(p.src[p.toks[start].start:last.end]), true
}

func (p *stmtParser) rawStatement() string {
	if len(p.toks) == 0 {
		return ""
	}
	return strings.TrimSpace(p.src[p.toks[0].start:p.toks[len(p.toks)-1].end])
}

// parseStatement parses one statement's tokens. It never returns an
// unclassified-vs-error decision ambiguously: any recognized ALTER TABLE
// sub-form that is missing required tokens is a *ParseError; anything
// else well-formed but not modeled becomes ir.OpUnclassified.
func parseStatement(file, src string, toks []token) (ir.MigrationOp, error) {
	p := &stmtParser{src: src, file: file, toks: toks}
	line := toks[0].line
	raw := p.rawStatement()

	if !p.acceptKeyword("ALTER") || !p.acceptKeyword("TABLE") {
		return unclassified(raw, line), nil
	}
	if p.acceptKeyword("IF") {
		if !p.acceptKeyword("EXISTS") {
			return ir.MigrationOp{}, &ParseError{File: file, Line: line, Message: "ALTER TABLE: expected IF EXISTS"}
		}
	}

	table, ok := p.name()
	if !ok {
		return ir.MigrationOp{}, &ParseError{File: file, Line: line, Message: "ALTER TABLE: expected table name"}
	}
	table = lastQualifiedPart(table)

	switch {
	case p.acceptKeyword("ADD"):
		return parseAddColumn(p, file, line, raw, table)
	case p.acceptKeyword("DROP"):
		return parseDropColumn(p, file, line, raw, table)
	case p.acceptKeyword("RENAME"):
		return parseRenameColumn(p, file, line, raw, table)
	case p.acceptKeyword("ALTER"):
		return parseAlterColumn(p, file, line, raw, table)
	default:
		return unclassified(raw, line), nil
	}
}

func unclassified(raw string, line int) ir.MigrationOp {
	return ir.MigrationOp{Kind: ir.OpUnclassified, RawStatement: raw, SourceLine: line}
}

// tableConstraintKeywords are the keywords that can follow ADD (without
// an explicit COLUMN keyword) to introduce a table constraint or
// like-clause rather than a column definition — e.g. "ADD CONSTRAINT
// ... UNIQUE (...)", "ADD PRIMARY KEY (...)", "ADD CHECK (...)". Since
// COLUMN is optional in Postgres' own grammar ("ADD [COLUMN] name type"
// is valid), a bare "ADD <ident>" is ambiguous until we check whether
// that identifier is one of these reserved shapes.
var tableConstraintKeywords = map[string]bool{
	"CONSTRAINT": true, "PRIMARY": true, "UNIQUE": true,
	"CHECK": true, "FOREIGN": true, "EXCLUDE": true, "LIKE": true,
}

func isTableConstraintKeyword(t token) bool {
	return t.kind == tokIdent && tableConstraintKeywords[strings.ToUpper(t.text)]
}

func parseAddColumn(p *stmtParser, file string, line int, raw, table string) (ir.MigrationOp, error) {
	explicitColumn := p.acceptKeyword("COLUMN")
	if !explicitColumn && isTableConstraintKeyword(p.peek()) {
		// "ADD CONSTRAINT ..." / "ADD PRIMARY KEY ..." / etc. — a real,
		// well-formed statement V1 does not model, not a malformed one.
		return unclassified(raw, line), nil
	}
	if p.acceptKeyword("IF") {
		if !p.acceptKeyword("NOT") || !p.acceptKeyword("EXISTS") {
			return ir.MigrationOp{}, &ParseError{File: file, Line: line, Message: "ALTER TABLE ... ADD COLUMN: expected IF NOT EXISTS"}
		}
	}
	column, ok := p.name()
	if !ok {
		if explicitColumn {
			// The COLUMN keyword unambiguously commits this statement to
			// column-definition syntax; a missing name past that point
			// is a genuine mistake, not an unsupported form.
			return ir.MigrationOp{}, &ParseError{File: file, Line: line, Message: fmt.Sprintf("ALTER TABLE %s ADD COLUMN: expected a column name", table)}
		}
		// Some other ADD form we don't recognize and COLUMN was never
		// confirmed — treat as unsupported rather than guessing.
		return unclassified(raw, line), nil
	}
	column = lastQualifiedPart(column)

	typ, ok := p.typeName()
	if !ok {
		return ir.MigrationOp{}, &ParseError{File: file, Line: line, Message: fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s: expected a type", table, column)}
	}

	nullable := true
	if p.acceptKeyword("NOT") {
		if !p.acceptKeyword("NULL") {
			return ir.MigrationOp{}, &ParseError{File: file, Line: line, Message: "ADD COLUMN: expected NOT NULL"}
		}
		nullable = false
	} else if p.acceptKeyword("NULL") {
		nullable = true
	}

	var def *string
	if p.acceptKeyword("DEFAULT") {
		expr, ok := p.restAsText()
		if !ok {
			return ir.MigrationOp{}, &ParseError{File: file, Line: line, Message: "ADD COLUMN: expected an expression after DEFAULT"}
		}
		def = &expr
	}

	op := ir.MigrationOp{
		Kind:         ir.OpAddColumn,
		Table:        table,
		Column:       column,
		NewType:      typ,
		Nullable:     nullable,
		Default:      def,
		RawStatement: raw,
		SourceLine:   line,
	}
	classifyAddColumn(&op)
	return op, nil
}

func parseDropColumn(p *stmtParser, file string, line int, raw, table string) (ir.MigrationOp, error) {
	if !p.acceptKeyword("COLUMN") {
		// DROP CONSTRAINT / DROP CHECK / etc. — unsupported, not malformed.
		return unclassified(raw, line), nil
	}
	if p.acceptKeyword("IF") {
		if !p.acceptKeyword("EXISTS") {
			return ir.MigrationOp{}, &ParseError{File: file, Line: line, Message: "DROP COLUMN: expected IF EXISTS"}
		}
	}
	column, ok := p.name()
	if !ok {
		return ir.MigrationOp{}, &ParseError{File: file, Line: line, Message: fmt.Sprintf("ALTER TABLE %s DROP COLUMN: expected a column name", table)}
	}
	column = lastQualifiedPart(column)

	op := ir.MigrationOp{
		Kind:         ir.OpDropColumn,
		Table:        table,
		Column:       column,
		RawStatement: raw,
		SourceLine:   line,
	}
	classifyDropColumn(&op)
	return op, nil
}

func parseRenameColumn(p *stmtParser, file string, line int, raw, table string) (ir.MigrationOp, error) {
	if !p.acceptKeyword("COLUMN") {
		// RENAME TO <new_table> / RENAME CONSTRAINT — unsupported.
		return unclassified(raw, line), nil
	}
	oldName, ok := p.name()
	if !ok {
		return ir.MigrationOp{}, &ParseError{File: file, Line: line, Message: fmt.Sprintf("ALTER TABLE %s RENAME COLUMN: expected a column name", table)}
	}
	oldName = lastQualifiedPart(oldName)
	if !p.acceptKeyword("TO") {
		return ir.MigrationOp{}, &ParseError{File: file, Line: line, Message: "RENAME COLUMN: expected TO"}
	}
	newName, ok := p.name()
	if !ok {
		return ir.MigrationOp{}, &ParseError{File: file, Line: line, Message: "RENAME COLUMN: expected a target column name"}
	}
	newName = lastQualifiedPart(newName)

	op := ir.MigrationOp{
		Kind:         ir.OpRenameColumn,
		Table:        table,
		Column:       oldName,
		NewColumn:    newName,
		RawStatement: raw,
		SourceLine:   line,
	}
	classifyRenameColumn(&op)
	return op, nil
}

func parseAlterColumn(p *stmtParser, file string, line int, raw, table string) (ir.MigrationOp, error) {
	p.acceptKeyword("COLUMN")
	column, ok := p.name()
	if !ok {
		return unclassified(raw, line), nil
	}
	column = lastQualifiedPart(column)

	switch {
	case p.acceptKeyword("TYPE"):
		return finishAlterColumnType(p, file, line, raw, table, column)

	case p.acceptKeyword("SET"):
		if p.acceptKeyword("DATA") {
			if !p.acceptKeyword("TYPE") {
				return ir.MigrationOp{}, &ParseError{File: file, Line: line, Message: "ALTER COLUMN ... SET DATA: expected TYPE"}
			}
			return finishAlterColumnType(p, file, line, raw, table, column)
		}
		if p.acceptKeyword("NOT") {
			if !p.acceptKeyword("NULL") {
				return ir.MigrationOp{}, &ParseError{File: file, Line: line, Message: "ALTER COLUMN ... SET NOT: expected NULL"}
			}
			op := ir.MigrationOp{Kind: ir.OpSetNotNull, Table: table, Column: column, RawStatement: raw, SourceLine: line}
			classifySetNotNull(&op)
			return op, nil
		}
		// SET DEFAULT / SET STATISTICS / SET STORAGE — unsupported.
		return unclassified(raw, line), nil

	case p.acceptKeyword("DROP"):
		if p.acceptKeyword("NOT") {
			if !p.acceptKeyword("NULL") {
				return ir.MigrationOp{}, &ParseError{File: file, Line: line, Message: "ALTER COLUMN ... DROP NOT: expected NULL"}
			}
			op := ir.MigrationOp{Kind: ir.OpDropNotNull, Table: table, Column: column, RawStatement: raw, SourceLine: line}
			classifyDropNotNull(&op)
			return op, nil
		}
		// DROP DEFAULT — unsupported.
		return unclassified(raw, line), nil

	default:
		return unclassified(raw, line), nil
	}
}

func finishAlterColumnType(p *stmtParser, file string, line int, raw, table, column string) (ir.MigrationOp, error) {
	typ, ok := p.typeName()
	if !ok {
		return ir.MigrationOp{}, &ParseError{File: file, Line: line, Message: fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE: expected a type", table, column)}
	}
	if p.acceptKeyword("USING") {
		if _, ok := p.restAsText(); !ok {
			return ir.MigrationOp{}, &ParseError{File: file, Line: line, Message: "ALTER COLUMN ... TYPE ... USING: expected an expression"}
		}
	}
	op := ir.MigrationOp{Kind: ir.OpAlterColumnType, Table: table, Column: column, NewType: typ, RawStatement: raw, SourceLine: line}
	classifyAlterColumnType(&op)
	return op, nil
}
