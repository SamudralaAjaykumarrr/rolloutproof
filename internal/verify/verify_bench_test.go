package verify

import (
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkRun_EndToEnd measures the full pipeline — real file I/O,
// real parsers, real graph construction, every implemented invariant
// family — against representative real fixtures, the actual cost a CI
// invocation or the cmd/eval harness pays per scenario.
func BenchmarkRun_EndToEnd(b *testing.B) {
	dirs := map[string]string{
		"safe_additive":        "safe/additive-column",
		"unsafe_flagship":      "unsafe/drop-column-before-drain",
		"unsafe_rollback":      "unsafe/rollback-after-irreversible-drop",
		"unsafe_api":           "unsafe/api-removed-response-field",
		"unknown_missing_meta": "unknown/missing-service-metadata",
	}
	wd, err := os.Getwd()
	if err != nil {
		b.Fatalf("Getwd: %v", err)
	}
	for name, dir := range dirs {
		full := filepath.Join(wd, "..", "..", "examples", dir)
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := Run(full); err != nil {
					b.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}
