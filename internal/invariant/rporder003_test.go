package invariant

import (
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// SC-UNSAFE-009-shaped: a destructive migration phased during rollout
// with no declared expand/contract sequencing. RP-ORDER-003 is the
// migration-timing-side counterpart to RP-K8S-004 and must fire on the
// same underlying plan, independent of the workload's own strategy.
func TestRPORDER003_AdvisoryUnsafeDestructiveDuringRollout(t *testing.T) {
	plan, g := flagshipGraph(t)
	diags := EvaluateOrder(plan, g, nil)
	d := diagFor(diags, RPORDER003)
	if d == nil || d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-ORDER-003 UNSAFE, got %+v", d)
	}
	if !d.Advisory {
		t.Fatalf("expected RP-ORDER-003 to be marked Advisory")
	}
	// An Advisory UNSAFE finding must not, by itself, flip the overall
	// aggregate — docs/invariants.md's own documented severity distinction.
	if got := Aggregate([]ir.Diagnostic{*d}); got != ir.VerdictSafe {
		t.Fatalf("expected an Advisory-only UNSAFE diagnostic to aggregate to SAFE, got %v", got)
	}
}

func TestRPORDER003_SafeWhenCoveredByExpandContract(t *testing.T) {
	plan, g := expandContractPlan(t, ir.PhaseDuringRollout)
	diags := EvaluateOrder(plan, g, nil)
	d := diagFor(diags, RPORDER003)
	if d == nil || d.Verdict != ir.VerdictSafe {
		t.Fatalf("expected RP-ORDER-003 SAFE when covered by an expand/contract declaration, got %+v", d)
	}
}

func TestRPORDER003_SafeWithNoMigrations(t *testing.T) {
	plan, g := checkoutPaymentsGraph(t, "v1")
	diags := EvaluateOrder(plan, g, nil)
	d := diagFor(diags, RPORDER003)
	if d == nil || d.Verdict != ir.VerdictSafe {
		t.Fatalf("expected RP-ORDER-003 SAFE (not applicable, no migrations), got %+v", d)
	}
}
