package invariant

import (
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

func setNotNullMigration(t *testing.T) ir.Migration {
	t.Helper()
	m, err := ir.NewMigration("032_require_email.sql", []ir.MigrationOp{
		{Kind: ir.OpSetNotNull, Table: "users", Column: "email", Destructiveness: ir.ConditionallyDestructive, Reversibility: ir.Reversible, SourceLine: 1},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return m
}

func notNullGraph(t *testing.T) (ir.RolloutPlan, *graph.Graph) {
	t.Helper()
	schema := mustSchema(t, mustTable(t, "users", []ir.Column{
		{Name: "id", Type: "integer"},
		{Name: "email", Type: "text", Nullable: true},
	}))
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: schema,
		Workloads:  []ir.WorkloadChange{{Workload: mustWorkload(t), FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: setNotNullMigration(t), Phase: ir.PhaseDuringRollout}},
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

// SC-UNSAFE-003 (docs/scenario-corpus.md): an old writer's contract
// declares it writes users rows but never asserts email is populated.
func TestRPDB004_UnsafeWhenOldWriterOmitsColumn(t *testing.T) {
	plan, g := notNullGraph(t)
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v1"}: mustService(t, "api", "v1", nil, []ir.ColumnRef{{Table: "users", Column: "id"}}),
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", nil, []ir.ColumnRef{{Table: "users", Column: "id"}, {Table: "users", Column: "email"}}),
	}
	diags := Evaluate(plan, g, services)
	var d *ir.Diagnostic
	for i := range diags {
		if diags[i].InvariantID == RPDB004 {
			d = &diags[i]
		}
	}
	if d == nil || d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-DB-004 UNSAFE, got %+v", d)
	}
	if d.Counterexample == nil || len(d.Evidence) == 0 {
		t.Fatalf("UNSAFE must always carry a counterexample and evidence")
	}
}

// SC-SAFE-009: a NOT NULL column added with a DEFAULT satisfies old
// writers automatically — Postgres supplies the value.
func TestRPDB004_SafeWithDefault(t *testing.T) {
	def := "'pending'"
	m, err := ir.NewMigration("025_require_status.sql", []ir.MigrationOp{
		{Kind: ir.OpAddColumn, Table: "orders", Column: "status", NewType: "text", Nullable: false, Default: &def, Destructiveness: ir.NonDestructive, Reversibility: ir.Reversible, SourceLine: 1},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	schema := mustSchema(t, mustTable(t, "orders", []ir.Column{{Name: "id", Type: "integer"}}))
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
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v1"}: mustService(t, "api", "v1", nil, []ir.ColumnRef{{Table: "orders", Column: "id"}}),
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", nil, []ir.ColumnRef{{Table: "orders", Column: "id"}}),
	}
	diags := Evaluate(plan, g, services)
	for _, d := range diags {
		if d.Verdict == ir.VerdictUnsafe {
			t.Fatalf("expected no UNSAFE diagnostics when a DEFAULT satisfies old writers, got %+v", d)
		}
	}
}

// A read-only version that never writes to the table cannot violate
// RP-DB-004, even though it "touches" the table via reads — WritesTable
// must be checked independently of TouchesTable.
func TestRPDB004_SafeForReadOnlyVersion(t *testing.T) {
	plan, g := notNullGraph(t)
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v1"}: mustService(t, "api", "v1", []ir.ColumnRef{{Table: "users", Column: "id"}}, nil),
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", []ir.ColumnRef{{Table: "users", Column: "id"}}, nil),
	}
	diags := Evaluate(plan, g, services)
	for _, d := range diags {
		if d.Verdict == ir.VerdictUnsafe {
			t.Fatalf("expected no UNSAFE diagnostics for a read-only version, got %+v", d)
		}
	}
}

func TestRPDB004_UnknownWhenTableNeverMentioned(t *testing.T) {
	plan, g := notNullGraph(t)
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v1"}: mustService(t, "api", "v1", nil, nil),
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", nil, nil),
	}
	diags := Evaluate(plan, g, services)
	for _, d := range diags {
		if d.InvariantID != RPDB004 {
			continue
		}
		if d.Verdict != ir.VerdictUnknown {
			t.Fatalf("expected RP-DB-004 UNKNOWN when contract metadata never mentions the table, got %v", d.Verdict)
		}
		if len(d.MissingEvidence) == 0 {
			t.Fatalf("UNKNOWN must always identify the missing evidence")
		}
	}
}
