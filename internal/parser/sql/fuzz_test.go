package sql

import (
	"strings"
	"testing"
)

// FuzzParse's only property is: Parse must never panic on arbitrary
// input, and must never return a *ParseError zero-value inconsistency
// (an error must always carry a message). It does not assert anything
// about which specific inputs succeed or fail — that is covered by the
// table tests in parser_test.go — only that the parser degrades to an
// error or ir.OpUnclassified, never a crash or a silently-wrong
// classification of unparseable input.
func FuzzParse(f *testing.F) {
	seeds := []string{
		`ALTER TABLE users ADD COLUMN email text;`,
		`ALTER TABLE users DROP COLUMN email;`,
		`ALTER TABLE users RENAME COLUMN email TO email_address;`,
		`ALTER TABLE users ALTER COLUMN id TYPE bigint;`,
		`ALTER TABLE users ALTER COLUMN email SET NOT NULL;`,
		`ALTER TABLE users ALTER COLUMN email DROP NOT NULL;`,
		`CREATE TABLE widgets (id serial primary key);`,
		`ALTER TABLE users ADD CONSTRAINT c UNIQUE (email);`,
		`'unterminated`,
		`"unterminated`,
		`/* unterminated`,
		``,
		`;;;`,
		`ALTER TABLE`,
		`ALTER TABLE users ADD COLUMN x text DEFAULT now()::text;`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, src string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Parse panicked on input %q: %v", src, r)
			}
		}()
		m, err := Parse("fuzz.sql", src)
		if err != nil {
			if pe, ok := err.(*ParseError); ok {
				if strings.TrimSpace(pe.Message) == "" {
					t.Fatalf("ParseError with empty message for input %q", src)
				}
			}
			return
		}
		for _, op := range m.Operations {
			if op.Kind.String() == "Unclassified" && op.RawStatement == "" {
				t.Fatalf("unclassified operation with no raw statement evidence for input %q", src)
			}
			if op.Kind == 0 && op.Kind.String() != "Unclassified" {
				t.Fatalf("zero-value OpKind must stringify as Unclassified")
			}
		}
	})
}
