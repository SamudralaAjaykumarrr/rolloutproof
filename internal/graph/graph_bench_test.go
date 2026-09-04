package graph

import (
	"fmt"
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// buildScalingPlan constructs a plan with numWorkloads concurrently
// changing RollingUpdate workloads and numDuringMigrations
// PhaseDuringRollout migrations — the two dimensions this package's own
// doc comment documents as O(3^W x 2^D) (docs/adr/0003).
func buildScalingPlan(b *testing.B, numWorkloads, numDuringMigrations int) ir.RolloutPlan {
	b.Helper()
	workloads := make([]ir.WorkloadChange, numWorkloads)
	for i := 0; i < numWorkloads; i++ {
		w, err := ir.NewWorkload(ir.Workload{
			Name: fmt.Sprintf("svc%d", i), ServiceName: fmt.Sprintf("svc%d", i), Version: "v2", Replicas: 3,
			Strategy: ir.RolloutStrategy{Type: ir.StrategyRollingUpdate, MaxSurge: ir.IntOrPercent{IntValue: 1}},
		})
		if err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
		workloads[i] = ir.WorkloadChange{Workload: w, FromVersion: "v1", ToVersion: "v2"}
	}

	cols := make([]ir.Column, numDuringMigrations)
	for i := range cols {
		cols[i] = ir.Column{Name: fmt.Sprintf("c%d", i), Type: "text", Nullable: true}
	}
	tab, err := ir.NewTable("t", cols)
	if err != nil {
		b.Fatalf("unexpected error: %v", err)
	}
	schema, err := ir.NewSchema([]ir.Table{tab})
	if err != nil {
		b.Fatalf("unexpected error: %v", err)
	}

	migrations := make([]ir.MigrationTiming, numDuringMigrations)
	for i := 0; i < numDuringMigrations; i++ {
		m, err := ir.NewMigration(fmt.Sprintf("m%d.sql", i), []ir.MigrationOp{
			{Kind: ir.OpDropColumn, Table: "t", Column: fmt.Sprintf("c%d", i), Destructiveness: ir.Destructive, Reversibility: ir.Irreversible},
		})
		if err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
		migrations[i] = ir.MigrationTiming{Migration: m, Phase: ir.PhaseDuringRollout}
	}

	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: schema,
		Workloads:  workloads,
		Migrations: migrations,
	})
	if err != nil {
		b.Fatalf("unexpected error: %v", err)
	}
	return plan
}

// BenchmarkBuild_Workloads scales W (concurrently-changing workloads)
// with D fixed at 0, isolating the 3^W dimension.
func BenchmarkBuild_Workloads(b *testing.B) {
	for _, w := range []int{1, 2, 3, 4} {
		plan := buildScalingPlan(b, w, 0)
		b.Run(fmt.Sprintf("W=%d", w), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := Build(plan); err != nil {
					b.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

// BenchmarkBuild_DuringMigrations scales D (PhaseDuringRollout
// migrations) with W fixed at 1, isolating the 2^D dimension.
func BenchmarkBuild_DuringMigrations(b *testing.B) {
	for _, d := range []int{1, 4, 8, 12} {
		plan := buildScalingPlan(b, 1, d)
		b.Run(fmt.Sprintf("D=%d", d), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := Build(plan); err != nil {
					b.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

// BenchmarkBuild_Combined scales both dimensions together, the shape a
// real multi-service rollout with several in-flight migrations would
// actually take.
func BenchmarkBuild_Combined(b *testing.B) {
	for _, tc := range []struct{ w, d int }{{1, 1}, {2, 2}, {3, 4}} {
		plan := buildScalingPlan(b, tc.w, tc.d)
		b.Run(fmt.Sprintf("W=%d,D=%d", tc.w, tc.d), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := Build(plan); err != nil {
					b.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

// BenchmarkShortestPath measures traversal cost once a graph is built —
// the per-invariant cost of selecting a counterexample
// (docs/architecture.md §4.4), separate from construction cost above.
func BenchmarkShortestPath(b *testing.B) {
	plan := buildScalingPlan(b, 2, 6)
	g, err := Build(plan)
	if err != nil {
		b.Fatalf("unexpected error: %v", err)
	}
	target := len(g.Nodes) - 1
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		g.ShortestPath(target)
	}
}
