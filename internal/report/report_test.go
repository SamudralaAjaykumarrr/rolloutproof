package report

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/invariant"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

var updateGolden = flag.Bool("update", false, "update golden files")

func mustSchema(t *testing.T, tables ...ir.Table) ir.Schema {
	t.Helper()
	s, err := ir.NewSchema(tables)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return s
}

func mustTable(t *testing.T, name string, cols []ir.Column) ir.Table {
	t.Helper()
	tab, err := ir.NewTable(name, cols)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return tab
}

func mustWorkload(t *testing.T) ir.Workload {
	t.Helper()
	w, err := ir.NewWorkload(ir.Workload{
		Name: "api", ServiceName: "api", Version: "v2", Replicas: 3,
		Strategy: ir.RolloutStrategy{Type: ir.StrategyRollingUpdate, MaxSurge: ir.IntOrPercent{IntValue: 1}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return w
}

func mustService(t *testing.T, name, version string, reads, writes []ir.ColumnRef) ir.Service {
	t.Helper()
	s, err := ir.NewService(name, version, reads, writes, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s.SourceFile = "contracts/" + name + "-" + version + ".yaml"
	return s
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("failed to update golden file: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read golden file %s: %v (run with -update to create it)", path, err)
	}
	if got != string(want) {
		t.Fatalf("output does not match golden file %s\n--- got ---\n%s\n--- want ---\n%s", path, got, string(want))
	}
}

func TestRenderText_Unsafe_Golden(t *testing.T) {
	migration, err := ir.NewMigration("017_drop_email.sql", []ir.MigrationOp{
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
		Migrations: []ir.MigrationTiming{{Migration: migration, Phase: ir.PhaseDuringRollout}},
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
	diags := invariant.Evaluate(g, services)
	checkGolden(t, "unsafe", RenderText(diags))
}

func TestRenderText_Safe_Golden(t *testing.T) {
	w := mustWorkload(t)
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: mustSchema(t),
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := graph.Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	diags := invariant.Evaluate(g, map[ir.ServiceKey]ir.Service{})
	checkGolden(t, "safe", RenderText(diags))
}

func TestRenderText_Unknown_Golden(t *testing.T) {
	migration, err := ir.NewMigration("060_drop_referral_code.sql", []ir.MigrationOp{
		{Kind: ir.OpDropColumn, Table: "users", Column: "referral_code", Destructiveness: ir.Destructive, Reversibility: ir.Irreversible, SourceLine: 1},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	schema := mustSchema(t, mustTable(t, "users", []ir.Column{
		{Name: "referral_code", Type: "text", Nullable: true},
	}))
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: schema,
		Workloads:  []ir.WorkloadChange{{Workload: mustWorkload(t), FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: migration, Phase: ir.PhaseDuringRollout}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := graph.Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No contract metadata at all for api@v1 or api@v2.
	diags := invariant.Evaluate(g, map[ir.ServiceKey]ir.Service{})
	checkGolden(t, "unknown", RenderText(diags))
}

func TestSummary_Deterministic(t *testing.T) {
	w := mustWorkload(t)
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: mustSchema(t),
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := graph.Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	diags := invariant.Evaluate(g, map[ir.ServiceKey]ir.Service{})
	a := Summary(diags)
	b := Summary(diags)
	if a != b {
		t.Fatalf("Summary must be deterministic: %q vs %q", a, b)
	}
	if a != "SAFE — 5 invariant(s) evaluated, 0 violated, 0 unknown" {
		t.Fatalf("unexpected summary: %q", a)
	}
}
