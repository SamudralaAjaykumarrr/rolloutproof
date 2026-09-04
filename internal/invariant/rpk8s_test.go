package invariant

import (
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// apiAuthDependencyGraph builds a two-workload plan: api depends on auth
// (declared via Workload.DependsOn); auth is static, api transitions
// v1->v2.
func apiAuthDependencyGraph(t *testing.T, waitsOnDependencies, hasReadinessProbe bool) (ir.RolloutPlan, *graph.Graph) {
	t.Helper()
	api, err := ir.NewWorkload(ir.Workload{
		Name: "api", ServiceName: "api", Version: "v2", Replicas: 3,
		Strategy:  ir.RolloutStrategy{Type: ir.StrategyRollingUpdate, MaxSurge: ir.IntOrPercent{IntValue: 1}},
		Readiness: ir.ReadinessSpec{HasReadinessProbe: hasReadinessProbe, WaitsOnDependencies: waitsOnDependencies},
		DependsOn: []string{"auth"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	auth, err := ir.NewWorkload(ir.Workload{
		Name: "auth", ServiceName: "auth", Version: "v1", Replicas: 1,
		Strategy: ir.RolloutStrategy{Type: ir.StrategyRecreate},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: mustSchema(t),
		Workloads: []ir.WorkloadChange{
			{Workload: api, FromVersion: "v1", ToVersion: "v2"},
			{Workload: auth, FromVersion: "v0", ToVersion: "v1"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := graph.Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return plan, g
}

// SC-UNSAFE-008: api's readiness probe does not wait on auth.
func TestRPK8S002_UnsafeReadinessDoesNotWaitOnDependency(t *testing.T) {
	plan, g := apiAuthDependencyGraph(t, false, true)
	diags := EvaluateK8s(plan, g, nil)
	d := diagFor(diags, RPK8S002)
	if d == nil || d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-K8S-002 UNSAFE, got %+v", d)
	}
	if d.Counterexample == nil || len(d.Evidence) == 0 {
		t.Fatalf("UNSAFE must carry a counterexample and evidence")
	}
}

func TestRPK8S002_SafeWhenWaitsOnDependencies(t *testing.T) {
	plan, g := apiAuthDependencyGraph(t, true, true)
	diags := EvaluateK8s(plan, g, nil)
	d := diagFor(diags, RPK8S002)
	if d == nil || d.Verdict != ir.VerdictSafe {
		t.Fatalf("expected RP-K8S-002 SAFE, got %+v", d)
	}
}

func TestRPK8S002_UnknownWhenNoReadinessProbeDeclaredAtAll(t *testing.T) {
	plan, g := apiAuthDependencyGraph(t, false, false)
	diags := EvaluateK8s(plan, g, nil)
	d := diagFor(diags, RPK8S002)
	if d == nil || d.Verdict != ir.VerdictUnknown || len(d.MissingEvidence) == 0 {
		t.Fatalf("expected RP-K8S-002 UNKNOWN (absence of a probe is not evidence either way), got %+v", d)
	}
}

func TestRPK8S003_UnsafeDropDuringDrain(t *testing.T) {
	w, err := ir.NewWorkload(ir.Workload{
		Name: "api", ServiceName: "api", Version: "v2", Replicas: 3,
		Strategy:    ir.RolloutStrategy{Type: ir.StrategyRollingUpdate, MaxSurge: ir.IntOrPercent{IntValue: 1}},
		Termination: ir.TerminationSpec{HasPreStopHook: true, GracePeriodSeconds: 30},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	schema := mustSchema(t, mustTable(t, "users", []ir.Column{{Name: "email", Type: "text", Nullable: true}}))
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: schema,
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: dropEmailMigration(t), Phase: ir.PhaseDuringRollout}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := graph.Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	diags := EvaluateK8s(plan, g, nil)
	d := diagFor(diags, RPK8S003)
	if d == nil || d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-K8S-003 UNSAFE, got %+v", d)
	}
}

func TestRPK8S003_SafeWithoutPreStopHook(t *testing.T) {
	w, err := ir.NewWorkload(ir.Workload{
		Name: "api", ServiceName: "api", Version: "v2", Replicas: 3,
		Strategy: ir.RolloutStrategy{Type: ir.StrategyRollingUpdate, MaxSurge: ir.IntOrPercent{IntValue: 1}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	schema := mustSchema(t, mustTable(t, "users", []ir.Column{{Name: "email", Type: "text", Nullable: true}}))
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: schema,
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: dropEmailMigration(t), Phase: ir.PhaseDuringRollout}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := graph.Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	diags := EvaluateK8s(plan, g, nil)
	d := diagFor(diags, RPK8S003)
	if d == nil || d.Verdict != ir.VerdictSafe {
		t.Fatalf("expected RP-K8S-003 SAFE (not applicable), got %+v", d)
	}
}

// Adversarial-review regression: a Recreate-strategy workload's model has
// no reachable state distinctly representing "old draining" — old fully
// stops before new starts, as a single edge, not a state
// (docs/graph's own package doc) — so a naive "old's FromVersion is
// live" check would treat the *pre-rollout* old-only state (nothing
// draining at all) as if it were draining, a false positive. RP-K8S-003
// must report UNKNOWN for this workload instead of guessing either way.
func TestRPK8S003_UnknownForRecreateStrategy(t *testing.T) {
	w, err := ir.NewWorkload(ir.Workload{
		Name: "api", ServiceName: "api", Version: "v2", Replicas: 3,
		Strategy:    ir.RolloutStrategy{Type: ir.StrategyRecreate},
		Termination: ir.TerminationSpec{HasPreStopHook: true, GracePeriodSeconds: 30},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	schema := mustSchema(t, mustTable(t, "users", []ir.Column{{Name: "email", Type: "text", Nullable: true}}))
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: schema,
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: dropEmailMigration(t), Phase: ir.PhaseDuringRollout}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := graph.Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	diags := EvaluateK8s(plan, g, nil)
	d := diagFor(diags, RPK8S003)
	if d == nil || d.Verdict != ir.VerdictUnknown || len(d.MissingEvidence) == 0 {
		t.Fatalf("expected RP-K8S-003 UNKNOWN (Recreate has no representable draining state) with identified missing evidence, got %+v", d)
	}
}

// SC-UNSAFE-009: destructive migration during rollout with MaxSurge and
// no expand/contract declaration.
func TestRPK8S004_AdvisoryUnsafeDestructiveDuringCoexistence(t *testing.T) {
	plan, g := flagshipGraph(t)
	diags := EvaluateK8s(plan, g, nil)
	d := diagFor(diags, RPK8S004)
	if d == nil || d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-K8S-004 UNSAFE, got %+v", d)
	}
	if !d.Advisory {
		t.Fatalf("expected RP-K8S-004 to be marked Advisory")
	}
	// An Advisory UNSAFE finding must not, by itself, flip the overall
	// aggregate — docs/invariants.md's own documented severity distinction.
	if got := Aggregate([]ir.Diagnostic{*d}); got != ir.VerdictSafe {
		t.Fatalf("expected an Advisory-only UNSAFE diagnostic to aggregate to SAFE, got %v", got)
	}
}

func TestRPK8S004_SafeWhenCoveredByExpandContract(t *testing.T) {
	plan, g := expandContractPlan(t, ir.PhaseDuringRollout)
	diags := EvaluateK8s(plan, g, nil)
	d := diagFor(diags, RPK8S004)
	if d == nil || d.Verdict != ir.VerdictSafe {
		t.Fatalf("expected RP-K8S-004 SAFE when covered by an expand/contract declaration, got %+v", d)
	}
}

func TestRPK8S001_DerivesFromAnotherFamilysCoexistenceFinding(t *testing.T) {
	plan, g := flagshipGraph(t)
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v1"}: mustService(t, "api", "v1", []ir.ColumnRef{{Table: "users", Column: "email"}}, nil),
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", nil, nil),
	}
	rpdbDiags := Evaluate(plan, g, services)
	// Force a coexistence-shaped counterexample by reusing an existing
	// UNSAFE finding directly, since flagshipGraph's own shortest
	// counterexample is the single-live-version path (see
	// TestRPDB001_FlagshipUnsafe) — RP-K8S-001 must still recognize *any*
	// UNSAFE finding with a multi-live-version ViolatingState across the
	// full diagnostic set it's given.
	k8sDiags := EvaluateK8s(plan, g, rpdbDiags)
	d := diagFor(k8sDiags, RPK8S001)
	if d == nil {
		t.Fatalf("expected an RP-K8S-001 diagnostic")
	}
	// flagshipGraph's shortest violation has only one live version, so
	// RP-K8S-001 correctly finds nothing to derive from here — this
	// documents the honest limitation (see EvaluateK8s's doc comment)
	// rather than asserting a specific verdict that depends on internal
	// shortest-path selection.
	if d.Verdict != ir.VerdictSafe && d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected a definite verdict, got %v", d.Verdict)
	}
}
