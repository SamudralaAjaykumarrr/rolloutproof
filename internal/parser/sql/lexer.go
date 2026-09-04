// Package sql parses a deliberately constrained subset of PostgreSQL DDL
// into ir.Migration values (docs/architecture.md §8, docs/vision.md §6).
//
// Supported statement forms (case-insensitive keywords, trailing
// semicolon optional on the final statement):
//
//	ALTER TABLE [IF EXISTS] <table> ADD COLUMN [IF NOT EXISTS] <column> <type> [NOT NULL] [DEFAULT <expr>]
//	ALTER TABLE [IF EXISTS] <table> DROP COLUMN [IF EXISTS] <column>
//	ALTER TABLE [IF EXISTS] <table> RENAME COLUMN <column> TO <new_column>
//	ALTER TABLE [IF EXISTS] <table> ALTER [COLUMN] <column> [SET DATA] TYPE <type> [USING <expr>]
//	ALTER TABLE [IF EXISTS] <table> ALTER [COLUMN] <column> SET NOT NULL
//	ALTER TABLE [IF EXISTS] <table> ALTER [COLUMN] <column> DROP NOT NULL
//
// A file may contain multiple statements separated by ';'. Any
// syntactically well-formed statement that does not match one of the
// forms above (CREATE TABLE, INSERT, ALTER TABLE ... ADD CONSTRAINT,
// ALTER TABLE ... RENAME TO, triggers, functions, DO blocks, etc.)
// produces an ir.MigrationOp with Kind == ir.OpUnclassified, carrying the
// raw statement text as evidence — it is never silently dropped and
// never guessed at (docs/vision.md §11).
//
// Known limitation: the lexer does not understand PostgreSQL
// dollar-quoted string bodies (`$$ ... $$`), used by CREATE FUNCTION /
// DO blocks. A semicolon inside such a body will be misread as a
// statement separator. V1 does not attempt to parse procedural SQL at
// all (docs/vision.md §6), so such statements were already going to be
// OpUnclassified; the practical consequence of this limitation is that a
// migration file mixing dollar-quoted bodies with supported ALTER TABLE
// statements may have its statement boundaries misdetected. Projects
// using V1 should keep procedural SQL in separate files from the ALTER
// TABLE statements RolloutProof evaluates.
package sql

import (
	"fmt"
	"strings"
)

// TokenKind classifies one lexical token.
type TokenKind int

const (
	tokEOF TokenKind = iota
	tokIdent
	tokQuotedIdent
	tokString
	tokNumber
	tokPunct // ( ) , ; .
	tokOp    // a run of operator symbols, e.g. "::", "=", "+" — opaque to
	// the parser, only ever consumed verbatim as part of a
	// DEFAULT/USING expression or a type name
)

// token is one lexical unit, with its exact source span so the parser can
// reconstruct raw substrings (e.g. a multi-word type name) without
// re-serializing from parsed fields.
type token struct {
	kind  TokenKind
	text  string // normalized text: for idents, as-written; for strings, the unescaped value
	raw   string // exact source substring, including quotes/escaping
	line  int
	start int
	end   int // exclusive
}

func (t token) is(kw string) bool {
	return t.kind == tokIdent && strings.EqualFold(t.text, kw)
}

// lexError reports a genuinely malformed lexical structure (unterminated
// string, unterminated quoted identifier, unterminated block comment) —
// distinct from "well-formed but unsupported statement", which is not an
// error at all (see package doc).
type lexError struct {
	Line    int
	Message string
}

func (e *lexError) Error() string {
	return fmt.Sprintf("sql: line %d: %s", e.Line, e.Message)
}

// lex tokenizes the full source into a token slice ending in a tokEOF
// sentinel. It returns a *lexError if the source contains an unterminated
// string, quoted identifier, or block comment.
func lex(src string) ([]token, error) {
	l := &lexer{src: src, line: 1}
	var toks []token
	for {
		tok, err := l.next()
		if err != nil {
			return nil, err
		}
		toks = append(toks, tok)
		if tok.kind == tokEOF {
			return toks, nil
		}
	}
}

type lexer struct {
	src  string
	pos  int
	line int
}

func (l *lexer) peekByte() byte {
	if l.pos >= len(l.src) {
		return 0
	}
	return l.src[l.pos]
}

func (l *lexer) peekByteAt(off int) byte {
	if l.pos+off >= len(l.src) {
		return 0
	}
	return l.src[l.pos+off]
}

func (l *lexer) advance() byte {
	b := l.src[l.pos]
	l.pos++
	if b == '\n' {
		l.line++
	}
	return b
}

func isIdentStart(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isIdentCont(b byte) bool {
	return isIdentStart(b) || (b >= '0' && b <= '9') || b == '$'
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

// opSymbol reports whether b is a character V1 accepts as part of an
// opaque operator/expression token (casts, comparisons, arithmetic) that
// can legally appear inside a DEFAULT or USING expression, which V1
// stores verbatim without interpreting. This is deliberately not the
// full set of bytes PostgreSQL's own lexer accepts in an operator — it is
// just enough to tokenize the expressions V1's test corpus and realistic
// migrations use without erroring, while still rejecting characters that
// signal genuinely malformed input (see the package doc's scope note).
func opSymbol(b byte) bool {
	switch b {
	case ':', '=', '<', '>', '!', '~', '+', '-', '*', '/', '%', '^', '&', '|', '@', '[', ']':
		return true
	default:
		return false
	}
}

func (l *lexer) skipWhitespaceAndComments() error {
	for {
		switch l.peekByte() {
		case ' ', '\t', '\r', '\n':
			l.advance()
		case '-':
			if l.peekByteAt(1) == '-' {
				for l.peekByte() != '\n' && l.peekByte() != 0 {
					l.advance()
				}
				continue
			}
			return nil
		case '/':
			if l.peekByteAt(1) == '*' {
				startLine := l.line
				l.advance()
				l.advance()
				closed := false
				for l.pos < len(l.src) {
					if l.peekByte() == '*' && l.peekByteAt(1) == '/' {
						l.advance()
						l.advance()
						closed = true
						break
					}
					l.advance()
				}
				if !closed {
					return &lexError{Line: startLine, Message: "unterminated block comment"}
				}
				continue
			}
			return nil
		default:
			return nil
		}
	}
}

func (l *lexer) next() (token, error) {
	if err := l.skipWhitespaceAndComments(); err != nil {
		return token{}, err
	}
	startLine := l.line
	start := l.pos
	if l.pos >= len(l.src) {
		return token{kind: tokEOF, line: startLine, start: start, end: start}, nil
	}

	b := l.peekByte()

	switch {
	case isIdentStart(b):
		for isIdentCont(l.peekByte()) {
			l.advance()
		}
		text := l.src[start:l.pos]
		return token{kind: tokIdent, text: text, raw: text, line: startLine, start: start, end: l.pos}, nil

	case isDigit(b):
		for isDigit(l.peekByte()) {
			l.advance()
		}
		if l.peekByte() == '.' && isDigit(l.peekByteAt(1)) {
			l.advance()
			for isDigit(l.peekByte()) {
				l.advance()
			}
		}
		text := l.src[start:l.pos]
		return token{kind: tokNumber, text: text, raw: text, line: startLine, start: start, end: l.pos}, nil

	case b == '\'':
		return l.lexString(startLine, start)

	case b == '"':
		return l.lexQuotedIdent(startLine, start)

	case b == '(' || b == ')' || b == ',' || b == ';' || b == '.':
		l.advance()
		text := l.src[start:l.pos]
		return token{kind: tokPunct, text: text, raw: text, line: startLine, start: start, end: l.pos}, nil

	case opSymbol(b):
		for opSymbol(l.peekByte()) {
			l.advance()
		}
		text := l.src[start:l.pos]
		return token{kind: tokOp, text: text, raw: text, line: startLine, start: start, end: l.pos}, nil

	default:
		l.advance()
		return token{}, &lexError{Line: startLine, Message: fmt.Sprintf("unexpected character %q", b)}
	}
}

func (l *lexer) lexString(startLine, start int) (token, error) {
	l.advance() // opening '
	var sb strings.Builder
	for {
		if l.pos >= len(l.src) {
			return token{}, &lexError{Line: startLine, Message: "unterminated string literal"}
		}
		if l.peekByte() == '\'' {
			if l.peekByteAt(1) == '\'' {
				sb.WriteByte('\'')
				l.advance()
				l.advance()
				continue
			}
			l.advance()
			break
		}
		sb.WriteByte(l.advance())
	}
	return token{kind: tokString, text: sb.String(), raw: l.src[start:l.pos], line: startLine, start: start, end: l.pos}, nil
}

func (l *lexer) lexQuotedIdent(startLine, start int) (token, error) {
	l.advance() // opening "
	var sb strings.Builder
	for {
		if l.pos >= len(l.src) {
			return token{}, &lexError{Line: startLine, Message: "unterminated quoted identifier"}
		}
		if l.peekByte() == '"' {
			if l.peekByteAt(1) == '"' {
				sb.WriteByte('"')
				l.advance()
				l.advance()
				continue
			}
			l.advance()
			break
		}
		sb.WriteByte(l.advance())
	}
	return token{kind: tokQuotedIdent, text: sb.String(), raw: l.src[start:l.pos], line: startLine, start: start, end: l.pos}, nil
}
