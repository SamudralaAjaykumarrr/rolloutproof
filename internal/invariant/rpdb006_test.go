package invariant

import (
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

func expandContractPlan(t *testing.T, contractPhase ir.MigrationPhase) (ir.RolloutPlan, *graph.Graph) {
	t.Helper()
	expand, err := ir.NewMigration("001_add_email_address.sql", []ir.MigrationOp{
		{Kind: ir.OpAddColumn, Table: "users", Column: "email_address", NewType: "text", Nullable: true, Destructiveness: ir.NonDestructive, Reversibility: ir.Reversible, SourceLine: 1},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	contract, err := ir.NewMigration("002_drop_email.sql", []ir.MigrationOp{
		{Kind: ir.OpDropColumn, Table: "users", Column: "email", Destructiveness: ir.Destructive, Reversibility: ir.Irreversible, SourceLine: 1},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	schema := mustSchema(t, mustTable(t, "users", []ir.Column{
		{Name: "id", Type: "integer"},
		{Name: "email", Type: "text", Nullable: true},
	}))
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: schema,
		Workloads:  []ir.WorkloadChange{{Workload: mustWorkload(t), FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{
			{Migration: expand, Phase: ir.PhaseBeforeRollout},
			{Migration: contract, Phase: contractPhase},
		},
		ExpandContractLinks: []ir.ExpandContractLink{
			{ExpandMigrationID: "001_add_email_address.sql", ContractMigrationID: "002_drop_email.sql"},
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

func TestRPDB006_SafeWhenContractIsAfterRollout(t *testing.T) {
	plan, g := expandContractPlan(t, ir.PhaseAfterRollout)
	diags := Evaluate(plan, g, map[ir.ServiceKey]ir.Service{})
	d := diagFor(diags, RPDB006)
	if d == nil || d.Verdict != ir.VerdictSafe {
		t.Fatalf("expected RP-DB-006 SAFE when the contract step is sequenced after full rollout, got %+v", d)
	}
}

// Unsafe example (docs/invariants.md RP-DB-006): the contract step's
// Phase is PhaseDuringRollout for the same rollout that introduces the
// consumer version change.
func TestRPDB006_UnsafeWhenContractIsDuringRollout(t *testing.T) {
	plan, g := expandContractPlan(t, ir.PhaseDuringRollout)
	diags := Evaluate(plan, g, map[ir.ServiceKey]ir.Service{})
	d := diagFor(diags, RPDB006)
	if d == nil || d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-DB-006 UNSAFE when the contract step is sequenced during rollout, got %+v", d)
	}
	if d.Counterexample == nil || len(d.Evidence) == 0 {
		t.Fatalf("UNSAFE must carry a counterexample and evidence")
	}
}

func TestRPDB006_NotApplicableWithoutDeclaredLink(t *testing.T) {
	plan, g := flagshipGraph(t)
	diags := Evaluate(plan, g, map[ir.ServiceKey]ir.Service{})
	d := diagFor(diags, RPDB006)
	if d == nil || d.Verdict != ir.VerdictSafe {
		t.Fatalf("expected RP-DB-006 SAFE (not applicable) with no declared link, got %+v", d)
	}
}

func TestNewRolloutPlan_RejectsMismatchedExpandContractLink(t *testing.T) {
	dropOnly, err := ir.NewMigration("a.sql", []ir.MigrationOp{
		{Kind: ir.OpDropColumn, Table: "users", Column: "x", Destructiveness: ir.Destructive, Reversibility: ir.Irreversible},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: mustSchema(t),
		Workloads:  []ir.WorkloadChange{{Workload: mustWorkload(t), FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: dropOnly, Phase: ir.PhaseAfterRollout}},
		ExpandContractLinks: []ir.ExpandContractLink{
			// Neither migration exists as an OpAddColumn "expand" step —
			// this must be rejected at construction, not silently accepted.
			{ExpandMigrationID: "a.sql", ContractMigrationID: "a.sql"},
		},
	})
	if err == nil {
		t.Fatalf("expected an error for an expand/contract link naming the same migration twice")
	}
}
