package invariant

import (
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// This file holds the five realistic, multi-layer adversarial case
// studies docs/ADVERSARIAL_REVIEW.md walks a reviewer through (Staff/SRE
// review phase, case studies A-E). Each one combines at least two
// invariant families rather than exercising a single rule in isolation,
// and every verdict asserted here is produced by the real engine — none
// are hand-typed expectations disconnected from an actual run.

// Case study A: a provider deploys v2, a DB migration destructively drops
// a column, an old consumer remains live, and the rollback target's own
// declared schema access depends on what the migration removed —
// multiple interacting hazards (RP-DB-001 and RP-ROLLBACK) from one plan.
func TestCaseStudy_A_MultipleInteractingHazards(t *testing.T) {
	w := mustWorkload(t) // RollingUpdate, coexistence-capable
	schema := mustSchema(t, mustTable(t, "users", []ir.Column{
		{Name: "id", Type: "integer"},
		{Name: "legacy_id", Type: "text", Nullable: true},
	}))
	dropLegacyID, err := ir.NewMigration("020_drop_legacy_id.sql", []ir.MigrationOp{
		{Kind: ir.OpDropColumn, Table: "users", Column: "legacy_id", Destructiveness: ir.Destructive, Reversibility: ir.Irreversible},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema:     schema,
		Workloads:      []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations:     []ir.MigrationTiming{{Migration: dropLegacyID, Phase: ir.PhaseDuringRollout}},
		RollbackTarget: &ir.RollbackTarget{Workload: w, ToVersion: "v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	g, err := graph.Build(plan)
	if err != nil {
		t.Fatal(err)
	}
	// v1 (both the old consumer AND the rollback target) still reads
	// legacy_id; v2 does not.
	v1 := mustService(t, "api", "v1", []ir.ColumnRef{{Table: "users", Column: "legacy_id"}}, nil)
	v2 := mustService(t, "api", "v2", []ir.ColumnRef{{Table: "users", Column: "id"}}, nil)
	services := map[ir.ServiceKey]ir.Service{v1.Key(): v1, v2.Key(): v2}

	forward := Evaluate(plan, g, services)
	if forwardVerdict := Aggregate(forward); forwardVerdict != ir.VerdictUnsafe {
		t.Fatalf("expected forward rollout UNSAFE (old consumer v1 still coexists while legacy_id is dropped), got %s: %+v", forwardVerdict, forward)
	}
	rpdb001 := diagFor(forward, RPDB001)
	if rpdb001 == nil || rpdb001.Verdict != ir.VerdictUnsafe || rpdb001.RollbackVerdict != ir.RollbackUnsafe {
		t.Fatalf("expected RP-DB-001 UNSAFE with RollbackVerdict UNSAFE (same column, same drop), got %+v", rpdb001)
	}

	rollback := EvaluateRollbackPlan(plan, g, services)
	rollback003 := diagFor(rollback, RPROLLBACK003)
	if rollback003 == nil || rollback003.Verdict != ir.VerdictUnsafe || rollback003.RollbackVerdict != ir.RollbackUnsafe {
		t.Fatalf("expected RP-ROLLBACK-003 to independently confirm the rollback target depends on the removed column, got %+v", rollback003)
	}
}

// Case study B: RollingUpdate allows coexistence, readiness admits
// traffic before a declared dependency is actually ready, AND the
// provider's contract changes underneath the still-live old consumer —
// a K8s-ordering hazard (RP-K8S-002) and an API-compatibility hazard
// (RP-API-001) present in the same plan, from different root causes.
func TestCaseStudy_B_ReadinessRaceAndProviderContractChange(t *testing.T) {
	front, err := ir.NewWorkload(ir.Workload{
		Name: "front", ServiceName: "front", Version: "v2", Replicas: 3,
		Strategy:  rollingCoexist(),
		Readiness: ir.ReadinessSpec{HasReadinessProbe: true, WaitsOnDependencies: false},
		DependsOn: []string{"back"},
	})
	if err != nil {
		t.Fatal(err)
	}
	back, err := ir.NewWorkload(ir.Workload{Name: "back", ServiceName: "back", Version: "v2", Replicas: 1, Strategy: ir.RolloutStrategy{Type: ir.StrategyRecreate}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: mustSchema(t, mustTable(t, "t", []ir.Column{{Name: "id", Type: "integer"}})),
		Workloads: []ir.WorkloadChange{
			{Workload: front, FromVersion: "v1", ToVersion: "v2"},
			{Workload: back, FromVersion: "v1", ToVersion: "v2"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	g, err := graph.Build(plan)
	if err != nil {
		t.Fatal(err)
	}

	reqShape, err := ir.NewShape([]ir.Field{{Name: "id", Required: true}})
	if err != nil {
		t.Fatal(err)
	}
	respShape, err := ir.NewShape(nil)
	if err != nil {
		t.Fatal(err)
	}
	// back@v2 removes the "GET /legacy" operation front@v1 still calls —
	// the provider contract change an old, still-live consumer is exposed
	// to during the very same coexistence window the readiness race opens.
	contractV1, err := ir.NewAPIContract("backapi", "back", "v1", []ir.Endpoint{{Operation: "GET /legacy", RequestShape: reqShape, ResponseShape: respShape}})
	if err != nil {
		t.Fatal(err)
	}
	contractV2, err := ir.NewAPIContract("backapi", "back", "v2", nil)
	if err != nil {
		t.Fatal(err)
	}
	contracts := map[ir.APIContractKey]ir.APIContract{contractV1.Key(): contractV1, contractV2.Key(): contractV2}

	frontV1, err := ir.NewServiceWithAPI("front", "v1", nil, nil, nil, nil,
		[]ir.APIConsumption{{ContractName: "backapi", Operation: "GET /legacy", RequestFieldsSent: []string{"id"}}})
	if err != nil {
		t.Fatal(err)
	}
	frontV2, err := ir.NewService("front", "v2", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	backV1, err := ir.NewServiceWithAPI("back", "v1", nil, nil, nil, []ir.APIProvision{{ContractName: "backapi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	backV2, err := ir.NewServiceWithAPI("back", "v2", nil, nil, nil, []ir.APIProvision{{ContractName: "backapi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	services := map[ir.ServiceKey]ir.Service{
		frontV1.Key(): frontV1, frontV2.Key(): frontV2, backV1.Key(): backV1, backV2.Key(): backV2,
	}

	var diags []ir.Diagnostic
	diags = append(diags, Evaluate(plan, g, services)...)
	diags = append(diags, EvaluateAPI(g, services, contracts)...)
	diags = append(diags, EvaluateOrder(g, services)...)
	diags = append(diags, EvaluateK8s(plan, g, diags)...)

	if overall := Aggregate(diags); overall != ir.VerdictUnsafe {
		t.Fatalf("expected UNSAFE (readiness race + provider contract change), got %s: %+v", overall, diags)
	}
	rpk8s002 := diagFor(diags, RPK8S002)
	if rpk8s002 == nil || rpk8s002.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-K8S-002 UNSAFE (front admits traffic without waiting on back's readiness), got %+v", rpk8s002)
	}
	rpapi001 := diagFor(diags, RPAPI001)
	if rpapi001 == nil || rpapi001.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-API-001 UNSAFE (back@v2 removes an operation front@v1 still calls), got %+v", rpapi001)
	}
}

// Case study C: forward rollout SAFE, but rollback UNSAFE — see
// TestMetamorphic_RollbackUnsafeDespiteForwardSafe (metamorphic_test.go),
// which is this exact case study, kept there because it is also the
// property regression test for "rollback safety must be evaluated
// independently of forward safety." Referenced here so a reviewer working
// through case studies A-E in order finds it without having to search:
// api@v2 never touches "legacy_flag" at all (forward-SAFE to drop it),
// but api@v1 — the rollback target — still reads it, so RP-ROLLBACK-003
// reports UNSAFE despite the forward pass being clean.

// Case study D: forward rollout UNSAFE as declared, but a safe ordering
// exists and the engine confirms it once that ordering is actually
// applied — the same migration, only its declared phase changes from
// "during" (coexistence window) to "after" (full drain first).
func TestCaseStudy_D_UnsafeOrderingHasASafeAlternative(t *testing.T) {
	w := mustWorkload(t)
	schema := mustSchema(t, mustTable(t, "users", []ir.Column{
		{Name: "id", Type: "integer"},
		{Name: "email", Type: "text", Nullable: true},
	}))
	v1 := mustService(t, "api", "v1", []ir.ColumnRef{{Table: "users", Column: "email"}}, nil)
	v2 := mustService(t, "api", "v2", []ir.ColumnRef{{Table: "users", Column: "id"}}, nil)
	services := map[ir.ServiceKey]ir.Service{v1.Key(): v1, v2.Key(): v2}

	unsafePlan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: schema,
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: dropEmailMigration(t), Phase: ir.PhaseDuringRollout}},
	})
	if err != nil {
		t.Fatal(err)
	}
	safePlan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: schema,
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: dropEmailMigration(t), Phase: ir.PhaseAfterRollout}},
	})
	if err != nil {
		t.Fatal(err)
	}

	unsafeGraph, err := graph.Build(unsafePlan)
	if err != nil {
		t.Fatal(err)
	}
	safeGraph, err := graph.Build(safePlan)
	if err != nil {
		t.Fatal(err)
	}

	unsafeVerdict := Aggregate(Evaluate(unsafePlan, unsafeGraph, services))
	safeVerdict := Aggregate(Evaluate(safePlan, safeGraph, services))

	if unsafeVerdict != ir.VerdictUnsafe {
		t.Fatalf("expected the declared (during-rollout) ordering to be UNSAFE, got %s", unsafeVerdict)
	}
	if safeVerdict != ir.VerdictSafe {
		t.Fatalf("expected the alternative (after-rollout) ordering to be confirmed SAFE by the real engine, got %s", safeVerdict)
	}
}

// Case study E: insufficient metadata forces UNKNOWN despite manifests
// that look entirely ordinary — a single additive ADD COLUMN migration
// (normally the textbook SAFE case, docs/scenario-corpus.md SC-SAFE-001),
// except the column is NOT NULL with no default (RP-DB-004's hazard
// shape) and no contract metadata exists for either service version at
// all, so the engine has no evidence either way and must not guess SAFE
// just because the manifests otherwise look harmless.
func TestCaseStudy_E_HarmlessLookingManifestsInsufficientMetadata(t *testing.T) {
	w := mustWorkload(t)
	addRequiredColumn, err := ir.NewMigration("021_add_required_tier.sql", []ir.MigrationOp{
		{Kind: ir.OpAddColumn, Table: "users", Column: "tier", NewType: "text", Nullable: false, Default: nil,
			Destructiveness: ir.ConditionallyDestructive, Reversibility: ir.Reversible},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: mustSchema(t, mustTable(t, "users", []ir.Column{{Name: "id", Type: "integer"}})),
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: addRequiredColumn, Phase: ir.PhaseDuringRollout}},
	})
	if err != nil {
		t.Fatal(err)
	}
	g, err := graph.Build(plan)
	if err != nil {
		t.Fatal(err)
	}

	// No contract metadata at all for either service version — the
	// "apparently harmless manifests, insufficient evidence" case.
	diags := Evaluate(plan, g, map[ir.ServiceKey]ir.Service{})
	if overall := Aggregate(diags); overall != ir.VerdictUnknown {
		t.Fatalf("expected UNKNOWN (no evidence either way about old writers), got %s: %+v", overall, diags)
	}
	rpdb004 := diagFor(diags, RPDB004)
	if rpdb004 == nil || rpdb004.Verdict != ir.VerdictUnknown || len(rpdb004.MissingEvidence) == 0 {
		t.Fatalf("expected RP-DB-004 UNKNOWN with a cited MissingEvidence gap, got %+v", rpdb004)
	}
}

func rollingCoexist() ir.RolloutStrategy {
	return ir.RolloutStrategy{Type: ir.StrategyRollingUpdate, MaxSurge: ir.IntOrPercent{IntValue: 1}}
}
