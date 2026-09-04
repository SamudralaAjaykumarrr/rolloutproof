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

// TestRun_EveryUnsafeCounterexampleHasAnOutcome guards against the class
// of bug this session found: a hardcoded, family-specific "runtime
// failure" string that was wrong for non-DB invariants. Every UNSAFE
// diagnostic's Counterexample must set its own Outcome — an empty one is
// a real invariant bug, not a cosmetic gap (ir.Counterexample's own doc
// comment).
func TestRun_EveryUnsafeCounterexampleHasAnOutcome(t *testing.T) {
	dirs := []string{
		"unsafe/drop-column-before-drain", "unsafe/rename-column-without-compat",
		"unsafe/not-null-without-default", "unsafe/narrowing-type-change",
		"unsafe/expand-contract-same-rollout", "unsafe/rollback-after-irreversible-drop",
		"unsafe/api-removed-response-field",
	}
	for _, dir := range dirs {
		diags, err := Run(exampleDir(t, dir))
		if err != nil {
			t.Fatalf("%s: Run: %v", dir, err)
		}
		for _, d := range diags {
			if d.Verdict != ir.VerdictUnsafe || d.Counterexample == nil {
				continue
			}
			if d.Counterexample.Outcome == "" {
				t.Errorf("%s: %s: UNSAFE counterexample has no Outcome", dir, d.InvariantID)
			}
		}
	}
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

// SC-UNSAFE-004: a new reader depends on a column scheduled to be added
// only after the rollout completes — the existence-check mechanism
// (evaluateColumnExistence) firing on a column that was never added yet,
// not one that was dropped.
func TestRun_UnsafeNewReaderBeforeMigration(t *testing.T) {
	diags, err := Run(exampleDir(t, "unsafe/new-reader-before-migration"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictUnsafe {
		t.Fatalf("expected UNSAFE, got %v (%+v)", got, diags)
	}
	d := diagFor(diags, invariant.RPDB001)
	if d == nil || d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-DB-001 UNSAFE, got %+v", d)
	}
	// Cross-layer: RP-K8S-001 should derive its own finding from
	// RP-DB-001's coexistence-shaped counterexample.
	k8s := diagFor(diags, invariant.RPK8S001)
	if k8s == nil || k8s.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-K8S-001 to derive UNSAFE from RP-DB-001's finding, got %+v", k8s)
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

func TestRun_SafeWideningTypeChange(t *testing.T) {
	diags, err := Run(exampleDir(t, "safe/widening-type-change"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictSafe {
		t.Fatalf("expected SAFE, got %v (%+v)", got, diags)
	}
}

func TestRun_UnsafeNarrowingTypeChange(t *testing.T) {
	diags, err := Run(exampleDir(t, "unsafe/narrowing-type-change"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictUnsafe {
		t.Fatalf("expected UNSAFE, got %v (%+v)", got, diags)
	}
	d := diagFor(diags, invariant.RPDB003)
	if d == nil || d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-DB-003 UNSAFE, got %+v", d)
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

func TestRun_SafeExpandContractSequenced(t *testing.T) {
	diags, err := Run(exampleDir(t, "safe/expand-contract-sequenced"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictSafe {
		t.Fatalf("expected SAFE, got %v (%+v)", got, diags)
	}
	d := diagFor(diags, invariant.RPDB006)
	if d == nil || d.Verdict != ir.VerdictSafe {
		t.Fatalf("expected RP-DB-006 SAFE, got %+v", d)
	}
}

func TestRun_UnsafeExpandContractSameRollout(t *testing.T) {
	diags, err := Run(exampleDir(t, "unsafe/expand-contract-same-rollout"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictUnsafe {
		t.Fatalf("expected UNSAFE, got %v (%+v)", got, diags)
	}
	d := diagFor(diags, invariant.RPDB006)
	if d == nil || d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-DB-006 UNSAFE, got %+v", d)
	}
}

// --- api: safe + unsafe + unknown ---

// SC-SAFE-003: provider adds an optional response field.
func TestRun_SafeAPIBackwardCompatibleAddition(t *testing.T) {
	diags, err := Run(exampleDir(t, "safe/api-backward-compatible-addition"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictSafe {
		t.Fatalf("expected SAFE, got %v (%+v)", got, diags)
	}
}

// SC-UNSAFE-006-shaped: provider renames a required response field while
// a live consumer still requires the old name.
func TestRun_UnsafeAPIRemovedResponseField(t *testing.T) {
	diags, err := Run(exampleDir(t, "unsafe/api-removed-response-field"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictUnsafe {
		t.Fatalf("expected UNSAFE, got %v (%+v)", got, diags)
	}
	d := diagFor(diags, invariant.RPAPI003)
	if d == nil || d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-API-003 UNSAFE, got %+v", d)
	}
}

func TestRun_UnknownAPIProviderContractMissing(t *testing.T) {
	diags, err := Run(exampleDir(t, "unknown/api-provider-not-in-plan"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictUnknown {
		t.Fatalf("expected UNKNOWN, got %v (%+v)", got, diags)
	}
	d := diagFor(diags, invariant.RPAPI001)
	if d == nil || d.Verdict != ir.VerdictUnknown || len(d.MissingEvidence) == 0 {
		t.Fatalf("expected RP-API-001 UNKNOWN with identified missing evidence, got %+v", d)
	}
}

// --- k8s: safe + unsafe ---

func TestRun_SafeK8sReadinessWaitsOnDependency(t *testing.T) {
	diags, err := Run(exampleDir(t, "safe/k8s-readiness-waits-on-dependency"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictSafe {
		t.Fatalf("expected SAFE, got %v (%+v)", got, diags)
	}
}

// SC-UNSAFE-008: readiness admits traffic before a declared dependency
// is ready.
func TestRun_UnsafeK8sReadinessBeforeDependency(t *testing.T) {
	diags, err := Run(exampleDir(t, "unsafe/k8s-readiness-before-dependency"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictUnsafe {
		t.Fatalf("expected UNSAFE, got %v (%+v)", got, diags)
	}
	d := diagFor(diags, invariant.RPK8S002)
	if d == nil || d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-K8S-002 UNSAFE, got %+v", d)
	}
}

// --- ordering: safe + unsafe ---

func TestRun_SafeOrderConsumerCompatible(t *testing.T) {
	diags, err := Run(exampleDir(t, "safe/order-consumer-compatible"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictSafe {
		t.Fatalf("expected SAFE, got %v (%+v)", got, diags)
	}
}

// SC-UNSAFE-011: checkout@v1 requires payments >= v3, but payments' own
// live versions during this rollout include v2 (the starting point).
func TestRun_UnsafeOrderConsumerBehindProvider(t *testing.T) {
	diags, err := Run(exampleDir(t, "unsafe/order-consumer-behind-provider"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictUnsafe {
		t.Fatalf("expected UNSAFE, got %v (%+v)", got, diags)
	}
	d := diagFor(diags, invariant.RPORDER001)
	if d == nil || d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-ORDER-001 UNSAFE, got %+v", d)
	}
}

// --- rollback: safe + unsafe + unknown ---

func TestRun_SafeRollbackAfterAdditiveOnly(t *testing.T) {
	diags, err := Run(exampleDir(t, "safe/rollback-after-additive-only"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictSafe {
		t.Fatalf("expected SAFE, got %v (%+v)", got, diags)
	}
	aggregate := diagFor(diags, invariant.RPROLLBACK003)
	if aggregate == nil || aggregate.RollbackVerdict != ir.RollbackSafe {
		t.Fatalf("expected RollbackVerdict SAFE, got %+v", aggregate)
	}
}

// SC-UNSAFE-010: rollback requested after an irreversible migration has
// committed.
func TestRun_UnsafeRollbackAfterIrreversibleDrop(t *testing.T) {
	diags, err := Run(exampleDir(t, "unsafe/rollback-after-irreversible-drop"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictUnsafe {
		t.Fatalf("expected UNSAFE, got %v (%+v)", got, diags)
	}
	rprb001 := diagFor(diags, invariant.RPROLLBACK001)
	if rprb001 == nil || rprb001.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-ROLLBACK-001 UNSAFE, got %+v", rprb001)
	}
	aggregate := diagFor(diags, invariant.RPROLLBACK003)
	if aggregate == nil || aggregate.RollbackVerdict != ir.RollbackUnsafe {
		t.Fatalf("expected RollbackVerdict UNSAFE, got %+v", aggregate)
	}
}

func TestRun_UnknownRollbackTargetContractMissing(t *testing.T) {
	diags, err := Run(exampleDir(t, "unknown/rollback-target-contract-missing"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictUnknown {
		t.Fatalf("expected UNKNOWN, got %v (%+v)", got, diags)
	}
	rpdb007 := diagFor(diags, invariant.RPDB007)
	if rpdb007 == nil || rpdb007.Verdict != ir.VerdictUnknown || len(rpdb007.MissingEvidence) == 0 {
		t.Fatalf("expected RP-DB-007 UNKNOWN with identified missing evidence, got %+v", rpdb007)
	}
	aggregate := diagFor(diags, invariant.RPROLLBACK003)
	if aggregate == nil || aggregate.RollbackVerdict != ir.RollbackUnknown {
		t.Fatalf("expected RollbackVerdict UNKNOWN, got %+v", aggregate)
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

// Adding a declared rollback dependency on a column that has already
// been irreversibly dropped must flip a plan from a rollback-SAFE
// verdict to a rollback-UNSAFE one — proving RollbackVerdict tracks
// declared facts, not the fixture's identity.
func TestRun_MutationAddingRollbackDependencyOnDroppedColumnFlipsToUnsafe(t *testing.T) {
	dir := t.TempDir()
	copyDir(t, exampleDir(t, "unsafe/drop-column-before-drain"), dir)
	replaceInFile(t, filepath.Join(dir, "rolloutplan.yaml"),
		"    phase: during\n",
		"    phase: during\n\nrollback:\n  toVersion: v1\n")

	diags, err := Run(dir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	aggregate := diagFor(diags, invariant.RPROLLBACK003)
	if aggregate == nil || aggregate.RollbackVerdict != ir.RollbackUnsafe {
		t.Fatalf("expected RollbackVerdict UNSAFE once a rollback to a version depending on the dropped column is declared, got %+v", aggregate)
	}
}

// Scheduling the same destructive migration strictly after full rollout
// completion (i.e., after old replicas have fully drained) instead of
// during it must flip UNSAFE to SAFE — SC-SAFE-006's mechanism, proven
// here as a mutation of the flagship UNSAFE fixture rather than a
// separately hand-built example, so the result is demonstrably caused by
// the phase change alone.
func TestRun_MutationDrainingBeforeDestructiveMigrationRemovesHazard(t *testing.T) {
	dir := t.TempDir()
	copyDir(t, exampleDir(t, "unsafe/drop-column-before-drain"), dir)
	replaceInFile(t, filepath.Join(dir, "rolloutplan.yaml"), "phase: during", "phase: after")
	// api@v2 must not itself depend on the dropped column, or the
	// after-rollout end state would independently violate RP-DB-001/002
	// too — matching contracts/api-v2.yaml's existing declaration, which
	// already excludes users.email.

	diags, err := Run(dir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invariant.Aggregate(diags); got != ir.VerdictSafe {
		t.Fatalf("expected SAFE once the migration is scheduled after full rollout completion, got %v (%+v)", got, diags)
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
