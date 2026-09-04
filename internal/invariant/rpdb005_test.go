package invariant

import (
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

func renameEmailMigration(t *testing.T) ir.Migration {
	t.Helper()
	m, err := ir.NewMigration("030_rename_email.sql", []ir.MigrationOp{
		{Kind: ir.OpRenameColumn, Table: "users", Column: "email", NewColumn: "email_address", Destructiveness: ir.Destructive, Reversibility: ir.Reversible, SourceLine: 1},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return m
}

func renameGraph(t *testing.T) *graph.Graph {
	t.Helper()
	schema := mustSchema(t, mustTable(t, "users", []ir.Column{
		{Name: "id", Type: "integer"},
		{Name: "email", Type: "text", Nullable: true},
	}))
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: schema,
		Workloads:  []ir.WorkloadChange{{Workload: mustWorkload(t), FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: renameEmailMigration(t), Phase: ir.PhaseDuringRollout}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := graph.Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return g
}

// SC-UNSAFE-002 (docs/scenario-corpus.md): a bare rename with no prior
// expand step, while api@v1 still reads the old column name.
func TestRPDB005_UnsafeFlagship(t *testing.T) {
	g := renameGraph(t)
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v1"}: mustService(t, "api", "v1", []ir.ColumnRef{{Table: "users", Column: "email"}}, nil),
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", []ir.ColumnRef{{Table: "users", Column: "email_address"}}, nil),
	}
	diags := Evaluate(g, services)
	var d *ir.Diagnostic
	for i := range diags {
		if diags[i].InvariantID == RPDB005 {
			d = &diags[i]
		}
	}
	if d == nil || d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-DB-005 UNSAFE, got %+v", d)
	}
	if d.Counterexample == nil || len(d.Evidence) == 0 {
		t.Fatalf("UNSAFE must always carry a counterexample and evidence")
	}
	if len(d.Counterexample.RecommendedSequence) == 0 {
		t.Fatalf("expected a recommended remediation sequence")
	}
}

// Once api@v1's dependency on the old name is removed (the expand/migrate
// step already completed), the same rename must report SAFE — proving the
// result is derived from facts, not the migration's shape alone.
func TestRPDB005_SafeWhenOldNameNoLongerReferenced(t *testing.T) {
	g := renameGraph(t)
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v1"}: mustService(t, "api", "v1", []ir.ColumnRef{{Table: "users", Column: "id"}}, nil),
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", []ir.ColumnRef{{Table: "users", Column: "email_address"}}, nil),
	}
	diags := Evaluate(g, services)
	for _, d := range diags {
		if d.Verdict == ir.VerdictUnsafe {
			t.Fatalf("expected no UNSAFE diagnostics once the old name is no longer referenced, got %+v", d)
		}
	}
}

func TestRPDB005_UnknownWhenContractMissing(t *testing.T) {
	g := renameGraph(t)
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", nil, nil),
	}
	diags := Evaluate(g, services)
	for _, d := range diags {
		if d.InvariantID != RPDB005 {
			continue
		}
		if d.Verdict != ir.VerdictUnknown {
			t.Fatalf("expected RP-DB-005 UNKNOWN when a live version's contract is entirely missing, got %v", d.Verdict)
		}
		if len(d.MissingEvidence) == 0 {
			t.Fatalf("UNKNOWN must always identify the missing evidence")
		}
	}
}

func TestRPDB005_RollbackClassifiedAgainstRenamedColumn(t *testing.T) {
	g := renameGraph(t)
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v1"}: mustService(t, "api", "v1", []ir.ColumnRef{{Table: "users", Column: "email"}}, nil),
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", []ir.ColumnRef{{Table: "users", Column: "email_address"}}, nil),
	}
	diags := Evaluate(g, services)
	for _, d := range diags {
		if d.InvariantID != RPDB005 {
			continue
		}
		if d.RollbackVerdict == ir.RollbackUnknown {
			t.Fatalf("expected rollback to be evaluated (not left at the zero value) for a rename hazard, got %v", d.RollbackVerdict)
		}
	}
}
