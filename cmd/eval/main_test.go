package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func examplesRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	return filepath.Join(wd, "..", "..", "examples")
}

// TestRun_AllScenariosPass is the harness's own regression: every fixture
// under examples/ must have its actual verdict match its directory's
// expected category. A failure here means either a real invariant
// regression or a fixture that was authored to demonstrate the wrong
// thing — both are exactly what this command exists to catch.
func TestRun_AllScenariosPass(t *testing.T) {
	var out bytes.Buffer
	code := run(examplesRoot(t), &out)
	if code != 0 {
		t.Fatalf("expected every scenario to pass, got exit code %d; output:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "0 failed") {
		t.Fatalf("expected \"0 failed\" in output, got:\n%s", out.String())
	}
}

func TestRun_ReportsFailureForMismatchedExpectation(t *testing.T) {
	// Build a temp examples/ tree that misdeclares an UNSAFE fixture as
	// safe/, so the harness must catch the mismatch rather than trusting
	// the directory name.
	root := t.TempDir()
	src := filepath.Join(examplesRoot(t), "unsafe", "drop-column-before-drain")
	dst := filepath.Join(root, "safe", "mislabeled")
	if err := copyTree(t, src, dst); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out bytes.Buffer
	code := run(root, &out)
	if code != 1 {
		t.Fatalf("expected exit code 1 for a mismatched scenario, got %d", code)
	}
	if !strings.Contains(out.String(), "FAIL") {
		t.Fatalf("expected FAIL in output, got:\n%s", out.String())
	}
}

func copyTree(t *testing.T, src, dst string) error {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyTree(t, srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
