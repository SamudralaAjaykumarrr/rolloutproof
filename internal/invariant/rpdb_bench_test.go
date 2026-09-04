package invariant

import (
	"fmt"
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// benchServices scales the number of distinct services declaring
// dependencies, isolating internal/invariant's own cost (service-map
// lookups and per-state scans) from internal/graph's construction cost
// (already covered by internal/graph's own scaling benchmarks).
func benchServices(b *testing.B, numDeps int) map[ir.ServiceKey]ir.Service {
	b.Helper()
	deps := make([]ir.ServiceDependency, numDeps)
	for i := 0; i < numDeps; i++ {
		deps[i] = ir.ServiceDependency{ServiceName: fmt.Sprintf("dep%d", i), MinCompatibleVersion: "v1"}
	}
	svc, err := ir.NewServiceWithAPI("api", "v1", []ir.ColumnRef{{Table: "users", Column: "email"}}, nil, deps, nil, nil)
	if err != nil {
		b.Fatalf("unexpected error: %v", err)
	}
	return map[ir.ServiceKey]ir.Service{{Name: "api", Version: "v1"}: svc}
}

func benchFlagshipGraph(b *testing.B) (ir.RolloutPlan, *graph.Graph) {
	b.Helper()
	w, err := ir.NewWorkload(ir.Workload{
		Name: "api", ServiceName: "api", Version: "v2", Replicas: 3,
		Strategy: ir.RolloutStrategy{Type: ir.StrategyRollingUpdate, MaxSurge: ir.IntOrPercent{IntValue: 1}},
	})
	if err != nil {
		b.Fatalf("unexpected error: %v", err)
	}
	m, err := ir.NewMigration("017_drop_email.sql", []ir.MigrationOp{
		{Kind: ir.OpDropColumn, Table: "users", Column: "email", Destructiveness: ir.Destructive, Reversibility: ir.Irreversible},
	})
	if err != nil {
		b.Fatalf("unexpected error: %v", err)
	}
	schema, err := ir.NewSchema([]ir.Table{mustBenchTable(b, "users")})
	if err != nil {
		b.Fatalf("unexpected error: %v", err)
	}
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: schema,
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: m, Phase: ir.PhaseDuringRollout}},
	})
	if err != nil {
		b.Fatalf("unexpected error: %v", err)
	}
	g, err := graph.Build(plan)
	if err != nil {
		b.Fatalf("unexpected error: %v", err)
	}
	return plan, g
}

func mustBenchTable(b *testing.B, name string) ir.Table {
	b.Helper()
	tab, err := ir.NewTable(name, []ir.Column{
		{Name: "id", Type: "integer"},
		{Name: "email", Type: "text", Nullable: true},
	})
	if err != nil {
		b.Fatalf("unexpected error: %v", err)
	}
	return tab
}

// BenchmarkEvaluate is the RP-DB family's cost against a fixed,
// realistic (flagship-shaped) graph — the per-invocation cost
// verify.Run pays on every call.
func BenchmarkEvaluate(b *testing.B) {
	plan, g := benchFlagshipGraph(b)
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v1"}: mustBenchService(b, "api", "v1", []ir.ColumnRef{{Table: "users", Column: "email"}}, nil),
		{Name: "api", Version: "v2"}: mustBenchService(b, "api", "v2", nil, nil),
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Evaluate(plan, g, services)
	}
}

func mustBenchService(b *testing.B, name, version string, reads, writes []ir.ColumnRef) ir.Service {
	b.Helper()
	s, err := ir.NewService(name, version, reads, writes, nil)
	if err != nil {
		b.Fatalf("unexpected error: %v", err)
	}
	return s
}

// BenchmarkEvaluateOrder_ServiceDependencies scales the number of
// declared dependencies on a single live consumer.
func BenchmarkEvaluateOrder_ServiceDependencies(b *testing.B) {
	for _, n := range []int{1, 10, 50} {
		plan, g := benchFlagshipGraph(b)
		services := benchServices(b, n)
		b.Run(fmt.Sprintf("deps=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				EvaluateOrder(plan, g, services)
			}
		})
	}
}
