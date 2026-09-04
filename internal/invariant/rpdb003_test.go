package invariant

import (
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

func typeChangeGraph(t *testing.T, oldType, newType string) (ir.RolloutPlan, *graph.Graph) {
	t.Helper()
	m, err := ir.NewMigration("022_change_user_id.sql", []ir.MigrationOp{
		{Kind: ir.OpAlterColumnType, Table: "users", Column: "user_id", NewType: newType, Destructiveness: ir.ConditionallyDestructive, Reversibility: ir.ConditionallyReversible, SourceLine: 1},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	schema := mustSchema(t, mustTable(t, "users", []ir.Column{{Name: "user_id", Type: oldType}}))
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: schema,
		Workloads:  []ir.WorkloadChange{{Workload: mustWorkload(t), FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: m, Phase: ir.PhaseDuringRollout}},
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

// SC-SAFE-008: integer -> bigint widening under coexistence.
func TestRPDB003_SafeWidening(t *testing.T) {
	plan, g := typeChangeGraph(t, "integer", "bigint")
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v1"}: mustService(t, "api", "v1", nil, []ir.ColumnRef{{Table: "users", Column: "user_id"}}),
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", nil, []ir.ColumnRef{{Table: "users", Column: "user_id"}}),
	}
	diags := Evaluate(plan, g, services)
	for _, d := range diags {
		if d.Verdict == ir.VerdictUnsafe {
			t.Fatalf("expected no UNSAFE for a widening type change, got %+v", d)
		}
	}
}

// Unsafe example (docs/invariants.md RP-DB-003): a narrowing change while
// a live version still reads/writes the column.
func TestRPDB003_UnsafeNarrowing(t *testing.T) {
	plan, g := typeChangeGraph(t, "bigint", "integer")
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v1"}: mustService(t, "api", "v1", nil, []ir.ColumnRef{{Table: "users", Column: "user_id"}}),
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", nil, []ir.ColumnRef{{Table: "users", Column: "user_id"}}),
	}
	diags := Evaluate(plan, g, services)
	d := diagFor(diags, RPDB003)
	if d == nil || d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-DB-003 UNSAFE, got %+v", d)
	}
	if d.Counterexample == nil || len(d.Evidence) == 0 {
		t.Fatalf("UNSAFE must carry a counterexample and evidence")
	}
}

// Unsafe example: numeric(10,2) -> integer, an Incomparable pair per the
// fixed table, must still be checked (fails toward "check it," not SAFE).
func TestRPDB003_UnsafeIncomparable(t *testing.T) {
	plan, g := typeChangeGraph(t, "numeric(10,2)", "integer")
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v1"}: mustService(t, "api", "v1", nil, []ir.ColumnRef{{Table: "users", Column: "user_id"}}),
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", nil, []ir.ColumnRef{{Table: "users", Column: "user_id"}}),
	}
	diags := Evaluate(plan, g, services)
	d := diagFor(diags, RPDB003)
	if d == nil || d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-DB-003 UNSAFE for an incomparable type pair touched by a live version, got %+v", d)
	}
}

func TestRPDB003_SafeWhenNoLiveVersionTouchesColumn(t *testing.T) {
	plan, g := typeChangeGraph(t, "bigint", "integer")
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v1"}: mustService(t, "api", "v1", nil, []ir.ColumnRef{{Table: "users", Column: "other"}}),
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", nil, []ir.ColumnRef{{Table: "users", Column: "other"}}),
	}
	diags := Evaluate(plan, g, services)
	d := diagFor(diags, RPDB003)
	if d == nil || d.Verdict != ir.VerdictSafe {
		t.Fatalf("expected RP-DB-003 SAFE when no live version touches the column, got %+v", d)
	}
}

func TestRPDB003_UnknownWhenPriorTypeUndeterminable(t *testing.T) {
	// A migration whose first statement is unclassified leaves every
	// subsequent operation's prior schema state indeterminate
	// (docs/architecture.md §8), so PriorType can never be captured for
	// the ALTER COLUMN TYPE that follows it.
	m, err := ir.NewMigration("099_mixed.sql", []ir.MigrationOp{
		{Kind: ir.OpUnclassified, RawStatement: "CREATE INDEX CONCURRENTLY ...", SourceLine: 1},
		{Kind: ir.OpAlterColumnType, Table: "users", Column: "user_id", NewType: "integer", Destructiveness: ir.ConditionallyDestructive, Reversibility: ir.ConditionallyReversible, SourceLine: 2},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	schema := mustSchema(t, mustTable(t, "users", []ir.Column{{Name: "user_id", Type: "bigint"}}))
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: schema,
		Workloads:  []ir.WorkloadChange{{Workload: mustWorkload(t), FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: m, Phase: ir.PhaseDuringRollout}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := graph.Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	diags := Evaluate(plan, g, map[ir.ServiceKey]ir.Service{})
	// Every reachable-state invariant should be UNKNOWN here: the schema
	// state itself is Indeterminate from the unclassified op onward.
	// RP-DB-006 is exempt — it is a structural, declaration-only check
	// over plan.ExpandContractLinks, not the reachable-state graph, and
	// this plan declares none, so it correctly reports "not applicable"
	// regardless of schema indeterminacy.
	for _, d := range diags {
		if d.InvariantID == RPDB006 {
			continue
		}
		if d.Verdict != ir.VerdictUnknown {
			t.Fatalf("expected UNKNOWN for %s when schema state is indeterminate, got %v", d.InvariantID, d.Verdict)
		}
	}
}
