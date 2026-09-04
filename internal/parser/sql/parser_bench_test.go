package sql

import (
	"fmt"
	"strings"
	"testing"
)

// representativeMigration mirrors a realistic multi-statement migration
// file — the mix of ADD/DROP/RENAME/ALTER TYPE/SET NOT NULL statements
// examples/*/migrations actually contain, repeated to a nontrivial size.
func representativeMigration(statements int) string {
	var b strings.Builder
	for i := 0; i < statements; i++ {
		fmt.Fprintf(&b, "ALTER TABLE users ADD COLUMN field_%d text;\n", i)
		fmt.Fprintf(&b, "ALTER TABLE users ALTER COLUMN field_%d SET NOT NULL;\n", i)
	}
	return b.String()
}

func BenchmarkParse(b *testing.B) {
	for _, n := range []int{1, 10, 100} {
		src := representativeMigration(n)
		b.Run(fmt.Sprintf("statements=%d", n*2), func(b *testing.B) {
			b.SetBytes(int64(len(src)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := Parse("bench.sql", src); err != nil {
					b.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}
