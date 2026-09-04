package verify

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/invariant"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// This file is RolloutProof's end-to-end proof (docs/architecture.md §9's
// "integration layer"): every case here runs the real directory
// convention through the real parsers (internal/parser/k8s,
// internal/parser/sql, internal/parser/contract, internal/parser/rolloutplan),
// the real transition graph (internal/graph), and the real invariant
// catalog (internal/invariant) — nothing here hand-constructs an
// ir.RolloutPlan or an ir.Service the way the package-level unit tests do.
// A result is asserted by verdict and (for UNSAFE) by which invariant ID
// fired, never by the fixture's directory name.

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	// internal/verify -> repo root
	return filepath.Join(wd, "..", "..")
}

func exampleDir(t *testing.T, rel string) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "examples", rel)
}

func diagFor(diags []ir.Diagnostic, id string) *ir.Diagnostic {
	for i := range diags {
		if diags[i].InvariantID == id {
			return &diags[i]
		}
	}
	return nil
}

func TestRun_SafeAdditiveColumn(t *testing.T) {
	diags, err := Run(exampleDir(t, "safe/additive-column"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictSafe {
		t.Fatalf("expected SAFE, got %v (%+v)", got, diags)
	}
}

func TestRun_UnsafeDropColumnBeforeDrain(t *testing.T) {
	diags, err := Run(exampleDir(t, "unsafe/drop-column-before-drain"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictUnsafe {
		t.Fatalf("expected UNSAFE, got %v (%+v)", got, diags)
	}
	// SC-UNSAFE-001 (docs/scenario-corpus.md): api@v1 still reads
	// users.email, dropped by 017_drop_email.sql.
	d := diagFor(diags, invariant.RPDB001)
	if d == nil || d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-DB-001 UNSAFE, got %+v", d)
	}
	if d.Counterexample == nil {
		t.Fatalf("UNSAFE must carry a counterexample")
	}
	if d.RollbackVerdict != ir.RollbackUnsafe {
		t.Fatalf("expected rollback UNSAFE (irreversible drop, old version depends on it), got %v", d.RollbackVerdict)
	}
}

func TestRun_UnknownMissingServiceMetadata(t *testing.T) {
	diags, err := Run(exampleDir(t, "unknown/missing-service-metadata"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictUnknown {
		t.Fatalf("expected UNKNOWN, got %v (%+v)", got, diags)
	}
	d := diagFor(diags, invariant.RPDB001)
	if d == nil || d.Verdict != ir.VerdictUnknown || len(d.MissingEvidence) == 0 {
		t.Fatalf("expected RP-DB-001 UNKNOWN with identified missing evidence, got %+v", d)
	}
}

func TestRun_UnsafeRenameColumnWithoutCompat(t *testing.T) {
	diags, err := Run(exampleDir(t, "unsafe/rename-column-without-compat"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictUnsafe {
		t.Fatalf("expected UNSAFE, got %v (%+v)", got, diags)
	}
	d := diagFor(diags, invariant.RPDB005)
	if d == nil || d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-DB-005 UNSAFE, got %+v", d)
	}
	if d.Counterexample == nil || len(d.Counterexample.RecommendedSequence) == 0 {
		t.Fatalf("UNSAFE must carry a counterexample with a recommended sequence")
	}
}

func TestRun_UnsafeNotNullWithoutDefault(t *testing.T) {
	diags, err := Run(exampleDir(t, "unsafe/not-null-without-default"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictUnsafe {
		t.Fatalf("expected UNSAFE, got %v (%+v)", got, diags)
	}
	d := diagFor(diags, invariant.RPDB004)
	if d == nil || d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-DB-004 UNSAFE, got %+v", d)
	}
}

// --- semantic mutation proof ---
//
// Both tests below start from the same real, on-disk UNSAFE fixture and
// change exactly one fact through the real parsers, proving the verdict
// is derived from that fact — not from the fixture's identity.

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
	if err != nil {
		t.Fatalf("copying fixture %s -> %s: %v", src, dst, err)
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func replaceInFile(t *testing.T, path, old, new string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("%s: expected to find %q to replace", path, old)
	}
	updated := strings.Replace(string(data), old, new, 1)
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// Removing api@v1's declared dependency on the dropped column must flip
// the same real pipeline from UNSAFE to SAFE — the mirror, at the
// filesystem/parser level, of internal/invariant's
// TestRPDB001_SafeWhenDependencyRemoved.
func TestRun_MutationRemovingDependencyFlipsToSafe(t *testing.T) {
	dir := t.TempDir()
	copyDir(t, exampleDir(t, "unsafe/drop-column-before-drain"), dir)
	replaceInFile(t, filepath.Join(dir, "contracts", "api-v1.yaml"),
		"  reads:\n    - users.id\n    - users.email\n",
		"  reads:\n    - users.id\n")

	diags, err := Run(dir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictSafe {
		t.Fatalf("expected SAFE once the dependency is removed, got %v (%+v)", got, diags)
	}
}

// Switching the rollout strategy to Recreate removes the coexistence
// state entirely, but must NOT by itself flip this hazard to SAFE:
// PhaseDuringRollout's declared commit-time ambiguity
// (docs/architecture.md §3.3, assumption A2) makes "migration committed
// while api@v1 is still the only live version" reachable regardless of
// strategy. This is the filesystem/parser-level mirror of
// internal/invariant's TestRPDB001_RecreateChangesReachabilityNotThisVerdict
// — proving the CLI-facing pipeline, not just the invariant engine in
// isolation, has this property.
func TestRun_MutationRecreateStrategyDoesNotChangeVerdict(t *testing.T) {
	dir := t.TempDir()
	copyDir(t, exampleDir(t, "unsafe/drop-column-before-drain"), dir)
	replaceInFile(t, filepath.Join(dir, "deployment.yaml"),
		"  strategy:\n    type: RollingUpdate\n    rollingUpdate:\n      maxSurge: 1\n      maxUnavailable: 0\n",
		"  strategy:\n    type: Recreate\n")

	diags, err := Run(dir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictUnsafe {
		t.Fatalf("expected UNSAFE to persist under Recreate (the hazard does not depend on coexistence), got %v (%+v)", got, diags)
	}
}
