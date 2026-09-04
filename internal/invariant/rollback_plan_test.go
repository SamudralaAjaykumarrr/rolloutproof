package invariant

import (
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

func TestEvaluateRollbackPlan_NotApplicableWithoutTarget(t *testing.T) {
	plan, g := flagshipGraph(t)
	diags := EvaluateRollbackPlan(plan, g, map[ir.ServiceKey]ir.Service{})
	if len(diags) != 4 {
		t.Fatalf("expected exactly 4 rollback diagnostics, got %d: %+v", len(diags), diags)
	}
	for _, d := range diags {
		if d.Verdict != ir.VerdictSafe {
			t.Fatalf("expected %s to report SAFE (not applicable) with no rollback target, got %v", d.InvariantID, d.Verdict)
		}
	}
}

// SC-UNSAFE-010 (docs/scenario-corpus.md): a rollback requested after an
// irreversible migration has committed, and the rollback target's
// declared schema access depends on what it removed.
func TestEvaluateRollbackPlan_UnsafeAfterIrreversibleMigration(t *testing.T) {
	w := mustWorkload(t)
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: mustSchema(t, mustTable(t, "users", []ir.Column{
			{Name: "id", Type: "integer"},
			{Name: "email", Type: "text", Nullable: true},
		})),
		Workloads:      []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations:     []ir.MigrationTiming{{Migration: dropEmailMigration(t), Phase: ir.PhaseDuringRollout}},
		RollbackTarget: &ir.RollbackTarget{Workload: w, ToVersion: "v1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := graph.Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v1"}: mustService(t, "api", "v1", []ir.ColumnRef{{Table: "users", Column: "email"}}, nil),
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", nil, nil),
	}
	diags := EvaluateRollbackPlan(plan, g, services)

	rpdb007 := diagFor(diags, RPDB007)
	if rpdb007 == nil || rpdb007.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-DB-007 UNSAFE, got %+v", rpdb007)
	}
	rprb001 := diagFor(diags, RPROLLBACK001)
	if rprb001 == nil || rprb001.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-ROLLBACK-001 UNSAFE (direct application of RP-DB-007), got %+v", rprb001)
	}
	aggregate := diagFor(diags, RPROLLBACK003)
	if aggregate == nil || aggregate.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-ROLLBACK-003 UNSAFE, got %+v", aggregate)
	}
	if aggregate.RollbackVerdict != ir.RollbackUnsafe {
		t.Fatalf("expected RollbackVerdict UNSAFE, got %v", aggregate.RollbackVerdict)
	}
	if aggregate.Counterexample == nil {
		t.Fatalf("expected the aggregate to carry a counterexample")
	}
}

// SC-SAFE-007: no migrations at all in the plan — rollback reduces to a
// pure WorkloadChange reversal, safe both forward and back.
func TestEvaluateRollbackPlan_SafeNoInterveningMigrations(t *testing.T) {
	w := mustWorkload(t)
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema:     mustSchema(t),
		Workloads:      []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		RollbackTarget: &ir.RollbackTarget{Workload: w, ToVersion: "v1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := graph.Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v1"}: mustService(t, "api", "v1", nil, nil),
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", nil, nil),
	}
	diags := EvaluateRollbackPlan(plan, g, services)
	aggregate := diagFor(diags, RPROLLBACK003)
	if aggregate == nil || aggregate.Verdict != ir.VerdictSafe {
		t.Fatalf("expected RP-ROLLBACK-003 SAFE, got %+v", aggregate)
	}
	if aggregate.RollbackVerdict != ir.RollbackSafe {
		t.Fatalf("expected RollbackVerdict SAFE, got %v", aggregate.RollbackVerdict)
	}
}

// Missing contract metadata for the rollback target itself must produce
// UNKNOWN, never a silent SAFE (docs/vision.md §10).
func TestEvaluateRollbackPlan_UnknownWhenRollbackTargetContractMissing(t *testing.T) {
	w := mustWorkload(t)
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: mustSchema(t, mustTable(t, "users", []ir.Column{
			{Name: "id", Type: "integer"},
			{Name: "email", Type: "text", Nullable: true},
		})),
		Workloads:      []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations:     []ir.MigrationTiming{{Migration: dropEmailMigration(t), Phase: ir.PhaseDuringRollout}},
		RollbackTarget: &ir.RollbackTarget{Workload: w, ToVersion: "v1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := graph.Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// api@v1 (the rollback target) has no contract at all.
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", nil, nil),
	}
	diags := EvaluateRollbackPlan(plan, g, services)
	rpdb007 := diagFor(diags, RPDB007)
	if rpdb007 == nil || rpdb007.Verdict != ir.VerdictUnknown || len(rpdb007.MissingEvidence) == 0 {
		t.Fatalf("expected RP-DB-007 UNKNOWN with identified missing evidence, got %+v", rpdb007)
	}
	aggregate := diagFor(diags, RPROLLBACK003)
	if aggregate == nil || aggregate.RollbackVerdict != ir.RollbackUnknown {
		t.Fatalf("expected RollbackVerdict UNKNOWN, got %+v", aggregate)
	}
}

// RP-ROLLBACK-002: even when the migration itself is technically
// reversible, if the rollback target's declared schema access still
// depends on the pre-change structure, restoring it against the current
// (un-reverted) schema fails. A NOT-NULL constraint is a clean example:
// no data is destroyed (RP-DB-007 has nothing to flag), but the rollback
// target — reusing api@v1's writer profile from before the constraint —
// would violate it.
func TestEvaluateRollbackPlan_UnsafeOldVersionAgainstNotNullConstraint(t *testing.T) {
	w := mustWorkload(t)
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: mustSchema(t, mustTable(t, "users", []ir.Column{
			{Name: "id", Type: "integer"},
			{Name: "email", Type: "text", Nullable: true},
		})),
		Workloads:      []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations:     []ir.MigrationTiming{{Migration: setNotNullMigration(t), Phase: ir.PhaseBeforeRollout}},
		RollbackTarget: &ir.RollbackTarget{Workload: w, ToVersion: "v1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := graph.Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	services := map[ir.ServiceKey]ir.Service{
		// api@v1's writer profile omits "email" — exactly the RP-DB-004
		// hazard, now evaluated against the rollback's own graph.
		{Name: "api", Version: "v1"}: mustService(t, "api", "v1", nil, []ir.ColumnRef{{Table: "users", Column: "id"}}),
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", nil, []ir.ColumnRef{{Table: "users", Column: "id"}, {Table: "users", Column: "email"}}),
	}
	diags := EvaluateRollbackPlan(plan, g, services)
	rprb002 := diagFor(diags, RPROLLBACK002)
	if rprb002 == nil || rprb002.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-ROLLBACK-002 UNSAFE, got %+v", rprb002)
	}
	aggregate := diagFor(diags, RPROLLBACK003)
	if aggregate == nil || aggregate.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-ROLLBACK-003 UNSAFE, got %+v", aggregate)
	}
}

func TestBuildRollback_ErrorsWithoutTarget(t *testing.T) {
	plan, g := flagshipGraph(t)
	if _, _, err := graph.BuildRollback(plan, g); err == nil {
		t.Fatalf("expected an error when forward plan declares no RollbackTarget")
	}
}

// Adversarial-review finding (false SAFE): graph.BuildRollback built the
// rollback's own RolloutPlan with no Migrations at all, so its graph's
// SchemaState.CommittedOps was always empty on every node even though
// BaseSchema correctly carried forward's resulting schema. RP-DB-003
// (evaluateTypeCompatibility) and RP-DB-005 (evaluateRenameWithoutCompatibility)
// both key their entire violation scan off CommittedOps, not the schema
// snapshot — unlike RP-DB-001/002/004, which check the snapshot directly
// and so kept working. The result: a type change committed anywhere in
// the forward rollout was invisible to RP-ROLLBACK-002's re-evaluation of
// the column-compatibility family against the rollback's own graph,
// regardless of whether the rollback target actually depended on the
// changed column under its old type. Fixed by having graph.BuildRollback
// carry forward's full CommittedOps trail (and Indeterminate flag, the
// same latent gap for the same reason) into the rollback graph's
// baseline, so history-dependent checks have the same evidence available
// against a rollback graph that they do against the forward one.
func TestEvaluateRollbackPlan_UnsafeOldVersionAgainstChangedColumnType(t *testing.T) {
	w := mustWorkload(t)
	amountToVarchar, err := ir.NewMigration("002_amount_to_varchar.sql", []ir.MigrationOp{
		{Kind: ir.OpAlterColumnType, Table: "orders", Column: "amount", NewType: "varchar(10)",
			Destructiveness: ir.ConditionallyDestructive, Reversibility: ir.ConditionallyReversible},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: mustSchema(t, mustTable(t, "orders", []ir.Column{
			{Name: "amount", Type: "integer", Nullable: true},
			{Name: "status", Type: "text", Nullable: true},
		})),
		Workloads:      []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations:     []ir.MigrationTiming{{Migration: amountToVarchar, Phase: ir.PhaseAfterRollout}},
		RollbackTarget: &ir.RollbackTarget{Workload: w, ToVersion: "v1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := graph.Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	services := map[ir.ServiceKey]ir.Service{
		// v1 (the rollback target) still reads/writes "amount" assuming its
		// original integer type; v2 touches the same table but a different
		// column, so it has no opinion about "amount" at all.
		{Name: "api", Version: "v1"}: mustService(t, "api", "v1",
			[]ir.ColumnRef{{Table: "orders", Column: "amount"}}, []ir.ColumnRef{{Table: "orders", Column: "amount"}}),
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2",
			[]ir.ColumnRef{{Table: "orders", Column: "status"}}, []ir.ColumnRef{{Table: "orders", Column: "status"}}),
	}
	diags := Evaluate(plan, g, services)
	diags = append(diags, EvaluateRollbackPlan(plan, g, services)...)
	if overall := Aggregate(diags); overall != ir.VerdictUnsafe {
		t.Fatalf("expected overall verdict UNSAFE (rollback target depends on a column whose type changed incompatibly), got %s", overall)
	}
	rprb002 := diagFor(diags, RPROLLBACK002)
	if rprb002 == nil || rprb002.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-ROLLBACK-002 UNSAFE, got %+v", rprb002)
	}
	aggregate := diagFor(diags, RPROLLBACK003)
	if aggregate == nil || aggregate.Verdict != ir.VerdictUnsafe || aggregate.RollbackVerdict != ir.RollbackUnsafe {
		t.Fatalf("expected RP-ROLLBACK-003 UNSAFE with RollbackVerdict UNSAFE, got %+v", aggregate)
	}
}
