package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exampleDir resolves an examples/ fixture path relative to the repo
// root, independent of the package's own directory.
func exampleDir(t *testing.T, rel string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	return filepath.Join(wd, "..", "..", "examples", rel)
}

// TestRun_ExitCodes is the CLI-level proof of docs/vision.md's exit-code
// contract: it drives run() in-process (no exec, so it exercises the
// exact binary logic without a subprocess) against the real example
// fixtures, checking the exit code the SAFE/UNSAFE/UNKNOWN scenario
// corpus (docs/scenario-corpus.md) prescribes.
func TestRun_ExitCodes(t *testing.T) {
	cases := []struct {
		name     string
		dir      string
		wantExit int
	}{
		{"safe", "safe/additive-column", exitSafe},
		{"safe widening type change", "safe/widening-type-change", exitSafe},
		{"safe expand/contract sequenced", "safe/expand-contract-sequenced", exitSafe},
		{"safe rollback after additive-only", "safe/rollback-after-additive-only", exitSafe},
		{"safe api backward compatible addition", "safe/api-backward-compatible-addition", exitSafe},
		{"unsafe drop column", "unsafe/drop-column-before-drain", exitUnsafe},
		{"unsafe rename", "unsafe/rename-column-without-compat", exitUnsafe},
		{"unsafe not null", "unsafe/not-null-without-default", exitUnsafe},
		{"unsafe narrowing type change", "unsafe/narrowing-type-change", exitUnsafe},
		{"unsafe expand/contract same rollout", "unsafe/expand-contract-same-rollout", exitUnsafe},
		{"unsafe rollback after irreversible drop", "unsafe/rollback-after-irreversible-drop", exitUnsafe},
		{"unsafe api removed response field", "unsafe/api-removed-response-field", exitUnsafe},
		{"unknown missing metadata", "unknown/missing-service-metadata", exitUnknown},
		{"unknown rollback target contract missing", "unknown/rollback-target-contract-missing", exitUnknown},
		{"unknown api provider not in plan", "unknown/api-provider-not-in-plan", exitUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			got := run([]string{"verify", exampleDir(t, tc.dir)}, &stdout, &stderr)
			if got != tc.wantExit {
				t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", got, tc.wantExit, stdout.String(), stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected no stderr output on a completed verification, got: %s", stderr.String())
			}
			if !strings.HasPrefix(stdout.String(), "ROLLOUT: ") {
				t.Fatalf("expected rendered output to start with the overall verdict, got:\n%s", stdout.String())
			}
		})
	}
}

func TestRun_UsageOnBadArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got := run(nil, &stdout, &stderr)
	if got != exitToolFailure {
		t.Fatalf("exit code = %d, want %d", got, exitToolFailure)
	}
	if stderr.Len() == 0 {
		t.Fatalf("expected usage text on stderr")
	}
}

func TestRun_ToolFailureOnMissingDirectory(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got := run([]string{"verify", filepath.Join(t.TempDir(), "does-not-exist")}, &stdout, &stderr)
	if got != exitToolFailure {
		t.Fatalf("exit code = %d, want %d", got, exitToolFailure)
	}
	if stderr.Len() == 0 {
		t.Fatalf("expected an error message on stderr")
	}
}

// TestRun_Deterministic proves the CLI's rendered output — not just the
// verdict — is byte-identical across repeated runs against the same
// input (docs/vision.md §9).
func TestRun_Deterministic(t *testing.T) {
	dir := exampleDir(t, "unsafe/drop-column-before-drain")
	var a, b bytes.Buffer
	var stderr bytes.Buffer
	run([]string{"verify", dir}, &a, &stderr)
	run([]string{"verify", dir}, &b, &stderr)
	if a.String() != b.String() {
		t.Fatalf("output is not deterministic across identical runs")
	}
}
