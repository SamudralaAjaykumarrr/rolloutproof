package invariant

import (
	"reflect"
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// This file holds metamorphic properties: relationships between two runs
// of the real engine over related-but-not-identical inputs, rather than
// a single run's verdict checked against a fixed expectation. Each one
// encodes a rule the project's own semantic model promises (docs/vision.md
// §10, docs/architecture.md §5-6) in a form independent of any single
// invariant's implementation.

// Property: removing a live version's declared dependency cannot create a
// new RP-ORDER hazard where none existed — RP-ORDER only ever fires on a
// version-constrained dependency that IS declared (anyDependencyDeclared),
// so removing the declaration can only move a Diagnostic from
// UNSAFE/UNKNOWN toward "not applicable" (SAFE), never the reverse.
func TestMetamorphic_RemovingDependencyNeverIntroducesOrderHazard(t *testing.T) {
	back, err := ir.NewWorkload(ir.Workload{Name: "back", ServiceName: "back", Version: "v1", Replicas: 1, Strategy: ir.RolloutStrategy{Type: ir.StrategyRecreate}})
	if err != nil {
		t.Fatal(err)
	}
	front, err := ir.NewWorkload(ir.Workload{Name: "front", ServiceName: "front", Version: "v2", Replicas: 1, Strategy: ir.RolloutStrategy{Type: ir.StrategyRecreate}})
	if err != nil {
		t.Fatal(err)
	}
	table := mustTable(t, "t", []ir.Column{{Name: "x", Type: "integer", Nullable: true}})
	schema := mustSchema(t, table)
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: schema,
		Workloads: []ir.WorkloadChange{
			{Workload: front, FromVersion: "v1", ToVersion: "v2"},
			{Workload: back, FromVersion: "v1", ToVersion: "v1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	g, err := graph.Build(plan)
	if err != nil {
		t.Fatal(err)
	}

	withDep, err := ir.NewService("front", "v2", nil, nil, []ir.ServiceDependency{{ServiceName: "back", MinCompatibleVersion: "v2"}})
	if err != nil {
		t.Fatal(err)
	}
	withoutDep, err := ir.NewService("front", "v2", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	backSvc, err := ir.NewService("back", "v1", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	servicesWithDep := map[ir.ServiceKey]ir.Service{withDep.Key(): withDep, backSvc.Key(): backSvc}
	servicesWithoutDep := map[ir.ServiceKey]ir.Service{withoutDep.Key(): withoutDep, backSvc.Key(): backSvc}

	withDiags := EvaluateOrder(g, servicesWithDep)
	withoutDiags := EvaluateOrder(g, servicesWithoutDep)

	withVerdict := diagFor(withDiags, RPORDER001).Verdict
	withoutVerdict := diagFor(withoutDiags, RPORDER001).Verdict

	if withVerdict != ir.VerdictUnsafe {
		t.Fatalf("test setup expected the declared dependency (back must be >= v2, but only v1 is live) to be UNSAFE, got %s", withVerdict)
	}
	if withoutVerdict != ir.VerdictSafe {
		t.Fatalf("removing the dependency declaration should make RP-ORDER-001 not-applicable (SAFE), got %s", withoutVerdict)
	}
}

// Property: a live consumer starting to satisfy a request/response field
// it previously omitted (strictly more compatibility evidence, nothing
// else about the plan changed) must not make the aggregate verdict less
// safe. Verdict dominance is UNSAFE > UNKNOWN > SAFE; "less safe" means
// moving toward UNSAFE.
func TestMetamorphic_AddingCompatibilityNeverWorsensVerdict(t *testing.T) {
	back, err := ir.NewWorkload(ir.Workload{Name: "back", ServiceName: "back", Version: "v1", Replicas: 1, Strategy: ir.RolloutStrategy{Type: ir.StrategyRecreate}})
	if err != nil {
		t.Fatal(err)
	}
	front, err := ir.NewWorkload(ir.Workload{Name: "front", ServiceName: "front", Version: "v2", Replicas: 1, Strategy: ir.RolloutStrategy{Type: ir.StrategyRecreate}})
	if err != nil {
		t.Fatal(err)
	}
	table := mustTable(t, "t", []ir.Column{{Name: "x", Type: "integer", Nullable: true}})
	schema := mustSchema(t, table)
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: schema,
		Workloads: []ir.WorkloadChange{
			{Workload: front, FromVersion: "v1", ToVersion: "v2"},
			{Workload: back, FromVersion: "v1", ToVersion: "v1"},
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
	contract, err := ir.NewAPIContract("backapi", "back", "v1", []ir.Endpoint{{Operation: "GET /x", RequestShape: reqShape, ResponseShape: respShape}})
	if err != nil {
		t.Fatal(err)
	}
	contracts := map[ir.APIContractKey]ir.APIContract{contract.Key(): contract}

	backSvc, err := ir.NewServiceWithAPI("back", "v1", nil, nil, nil, []ir.APIProvision{{ContractName: "backapi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	omitsField, err := ir.NewServiceWithAPI("front", "v2", nil, nil, nil, nil,
		[]ir.APIConsumption{{ContractName: "backapi", Operation: "GET /x", RequestFieldsSent: nil}})
	if err != nil {
		t.Fatal(err)
	}
	sendsField, err := ir.NewServiceWithAPI("front", "v2", nil, nil, nil, nil,
		[]ir.APIConsumption{{ContractName: "backapi", Operation: "GET /x", RequestFieldsSent: []string{"id"}}})
	if err != nil {
		t.Fatal(err)
	}

	servicesOmit := map[ir.ServiceKey]ir.Service{omitsField.Key(): omitsField, backSvc.Key(): backSvc}
	servicesSend := map[ir.ServiceKey]ir.Service{sendsField.Key(): sendsField, backSvc.Key(): backSvc}

	omitVerdict := Aggregate(EvaluateAPI(g, servicesOmit, contracts))
	sendVerdict := Aggregate(EvaluateAPI(g, servicesSend, contracts))

	if omitVerdict != ir.VerdictUnsafe {
		t.Fatalf("test setup expected omitting the required field to be UNSAFE, got %s", omitVerdict)
	}
	if sendVerdict == ir.VerdictUnsafe {
		t.Fatalf("sending the previously-omitted required field (strictly more compatibility evidence) must not leave the verdict UNSAFE, got %s", sendVerdict)
	}
}

// Property: fully draining the old version before a destructive migration
// commits (PhaseAfterRollout instead of PhaseDuringRollout with
// coexistence) must remove the coexistence-specific RP-DB-001 hazard, when
// nothing else about the plan changes.
func TestMetamorphic_DrainingBeforeDestructiveMigrationRemovesHazard(t *testing.T) {
	w := mustWorkload(t)
	schema := mustSchema(t, mustTable(t, "users", []ir.Column{
		{Name: "id", Type: "integer"},
		{Name: "email", Type: "text", Nullable: true},
	}))
	v1 := mustService(t, "api", "v1", []ir.ColumnRef{{Table: "users", Column: "email"}}, nil)
	v2 := mustService(t, "api", "v2", nil, nil)
	services := map[ir.ServiceKey]ir.Service{v1.Key(): v1, v2.Key(): v2}

	duringPlan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: schema,
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: dropEmailMigration(t), Phase: ir.PhaseDuringRollout}},
	})
	if err != nil {
		t.Fatal(err)
	}
	afterPlan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: schema,
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: dropEmailMigration(t), Phase: ir.PhaseAfterRollout}},
	})
	if err != nil {
		t.Fatal(err)
	}

	duringGraph, err := graph.Build(duringPlan)
	if err != nil {
		t.Fatal(err)
	}
	afterGraph, err := graph.Build(afterPlan)
	if err != nil {
		t.Fatal(err)
	}

	duringVerdict := diagFor(Evaluate(duringPlan, duringGraph, services), RPDB001).Verdict
	afterVerdict := diagFor(Evaluate(afterPlan, afterGraph, services), RPDB001).Verdict

	if duringVerdict != ir.VerdictUnsafe {
		t.Fatalf("test setup expected dropping a column still read by a coexisting old version to be UNSAFE, got %s", duringVerdict)
	}
	if afterVerdict != ir.VerdictSafe {
		t.Fatalf("draining v1 fully before the drop commits (phase after rollout) should remove this specific coexistence hazard, got %s", afterVerdict)
	}
}

// Property: removing the only evidence that made a violation detectable
// must degrade that Diagnostic to UNKNOWN, never fabricate SAFE — the
// single most important safety property this project has (docs/vision.md
// §10). Verified here by literally deleting the violating service version
// from the services map RP-DB-001 was evaluated against.
func TestMetamorphic_RemovingEvidenceDegradesToUnknownNeverSafe(t *testing.T) {
	plan, g := flagshipGraph(t)
	v1 := mustService(t, "api", "v1", []ir.ColumnRef{{Table: "users", Column: "email"}}, nil)
	v2 := mustService(t, "api", "v2", nil, nil)

	withEvidence := map[ir.ServiceKey]ir.Service{v1.Key(): v1, v2.Key(): v2}
	withoutEvidence := map[ir.ServiceKey]ir.Service{v2.Key(): v2} // v1's contract metadata is now "missing"

	withVerdict := diagFor(Evaluate(plan, g, withEvidence), RPDB001).Verdict
	withoutDiag := diagFor(Evaluate(plan, g, withoutEvidence), RPDB001)

	if withVerdict != ir.VerdictUnsafe {
		t.Fatalf("test setup expected UNSAFE with full evidence, got %s", withVerdict)
	}
	if withoutDiag.Verdict == ir.VerdictSafe {
		t.Fatalf("removing v1's contract metadata must not fabricate SAFE, got %s (MissingEvidence: %+v)", withoutDiag.Verdict, withoutDiag.MissingEvidence)
	}
	if withoutDiag.Verdict != ir.VerdictUnknown {
		t.Fatalf("expected UNKNOWN specifically once v1's evidence is gone, got %s", withoutDiag.Verdict)
	}
	if len(withoutDiag.MissingEvidence) == 0 {
		t.Fatalf("an UNKNOWN diagnostic must cite what evidence is missing, got none")
	}
}

// Property: rollback safety is evaluated independently of forward
// safety — a forward-SAFE plan can still have an UNSAFE rollback, and the
// aggregate forward verdict must not launder that away.
func TestMetamorphic_RollbackUnsafeDespiteForwardSafe(t *testing.T) {
	w := mustWorkload(t)
	schema := mustSchema(t, mustTable(t, "users", []ir.Column{
		{Name: "id", Type: "integer"},
		{Name: "legacy_flag", Type: "text", Nullable: true},
	}))
	// v2 (forward target) touches "users" (via "id", so it isn't an
	// evidence gap) but never legacy_flag, so dropping legacy_flag is
	// forward-SAFE; v1 (the rollback target) still reads legacy_flag, so
	// rolling back after the drop commits is unsafe.
	v1 := mustService(t, "api", "v1", []ir.ColumnRef{{Table: "users", Column: "legacy_flag"}}, nil)
	v2 := mustService(t, "api", "v2", []ir.ColumnRef{{Table: "users", Column: "id"}}, nil)
	services := map[ir.ServiceKey]ir.Service{v1.Key(): v1, v2.Key(): v2}

	dropLegacy, err := ir.NewMigration("003_drop_legacy_flag.sql", []ir.MigrationOp{
		{Kind: ir.OpDropColumn, Table: "users", Column: "legacy_flag", Destructiveness: ir.Destructive, Reversibility: ir.Irreversible},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema:     schema,
		Workloads:      []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations:     []ir.MigrationTiming{{Migration: dropLegacy, Phase: ir.PhaseAfterRollout}},
		RollbackTarget: &ir.RollbackTarget{Workload: w, ToVersion: "v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	g, err := graph.Build(plan)
	if err != nil {
		t.Fatal(err)
	}

	forwardDiags := Evaluate(plan, g, services)
	forwardVerdict := Aggregate(forwardDiags)
	if forwardVerdict != ir.VerdictSafe {
		t.Fatalf("expected forward pass SAFE (v2 never touches legacy_flag), got %s: %+v", forwardVerdict, forwardDiags)
	}

	rollbackDiags := EvaluateRollbackPlan(plan, g, services)
	rollback003 := diagFor(rollbackDiags, RPROLLBACK003)
	if rollback003 == nil || rollback003.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-ROLLBACK-003 UNSAFE despite forward being SAFE, got %+v", rollback003)
	}

	overall := Aggregate(append(append([]ir.Diagnostic(nil), forwardDiags...), rollbackDiags...))
	if overall != ir.VerdictUnsafe {
		t.Fatalf("expected the combined overall verdict to surface the rollback hazard as UNSAFE, got %s", overall)
	}
}

// Property: an operation the SQL parser could not classify (ir.OpUnclassified)
// must never let the affected schema state read as SAFE — it must
// propagate as UNKNOWN (via SchemaState.Indeterminate) through every
// invariant that would otherwise have evaluated that state.
func TestMetamorphic_UnclassifiedOperationNeverFabricatesSafe(t *testing.T) {
	w := mustWorkload(t)
	schema := mustSchema(t, mustTable(t, "users", []ir.Column{{Name: "id", Type: "integer"}}))
	unclassified, err := ir.NewMigration("999_mystery.sql", []ir.MigrationOp{
		{Kind: ir.OpUnclassified, Table: "users", RawStatement: "CREATE INDEX CONCURRENTLY idx_users_id ON users (id);"},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: schema,
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: unclassified, Phase: ir.PhaseDuringRollout}},
	})
	if err != nil {
		t.Fatal(err)
	}
	g, err := graph.Build(plan)
	if err != nil {
		t.Fatal(err)
	}
	v1 := mustService(t, "api", "v1", []ir.ColumnRef{{Table: "users", Column: "id"}}, nil)
	v2 := mustService(t, "api", "v2", []ir.ColumnRef{{Table: "users", Column: "id"}}, nil)
	services := map[ir.ServiceKey]ir.Service{v1.Key(): v1, v2.Key(): v2}

	diags := Evaluate(plan, g, services)
	overall := Aggregate(diags)
	if overall == ir.VerdictSafe {
		t.Fatalf("an unclassified migration operation must never allow the overall verdict to be SAFE, got %s: %+v", overall, diags)
	}
}

// Property: identical semantic facts constructed in a different order
// (services map insertion order, workload slice order) must produce
// byte-for-byte identical diagnostics — determinism is meaningless if it
// only holds for one specific construction order.
func TestMetamorphic_ConstructionOrderInvariance(t *testing.T) {
	plan, g := flagshipGraph(t)
	v1 := mustService(t, "api", "v1", []ir.ColumnRef{{Table: "users", Column: "email"}}, nil)
	v2 := mustService(t, "api", "v2", nil, nil)

	order1 := map[ir.ServiceKey]ir.Service{v1.Key(): v1, v2.Key(): v2}
	order2 := map[ir.ServiceKey]ir.Service{v2.Key(): v2, v1.Key(): v1}

	diags1 := Evaluate(plan, g, order1)
	diags2 := Evaluate(plan, g, order2)
	if !reflect.DeepEqual(diags1, diags2) {
		t.Fatalf("expected identical diagnostics regardless of services map construction order:\n%+v\nvs\n%+v", diags1, diags2)
	}
}
