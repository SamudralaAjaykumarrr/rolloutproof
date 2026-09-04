package graph

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

func mustWorkload(t *testing.T, strategy ir.RolloutStrategy) ir.Workload {
	t.Helper()
	w, err := ir.NewWorkload(ir.Workload{
		Name: "api", ServiceName: "api", Version: "v2", Replicas: 3, Strategy: strategy,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return w
}

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

func rollingUpdateStrategy() ir.RolloutStrategy {
	return ir.RolloutStrategy{Type: ir.StrategyRollingUpdate, MaxSurge: ir.IntOrPercent{IntValue: 1}}
}

func noCoexistenceRollingStrategy() ir.RolloutStrategy {
	return ir.RolloutStrategy{Type: ir.StrategyRollingUpdate, MaxUnavailable: ir.IntOrPercent{IntValue: 3}}
}

func dropEmailMigration(t *testing.T) ir.Migration {
	t.Helper()
	m, err := ir.NewMigration("017_drop_email.sql", []ir.MigrationOp{
		{Kind: ir.OpDropColumn, Table: "users", Column: "email", Destructiveness: ir.Destructive, Reversibility: ir.Irreversible},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return m
}

func baseSchemaWithEmail(t *testing.T) ir.Schema {
	return mustSchema(t, mustTable(t, "users", []ir.Column{
		{Name: "id", Type: "integer"},
		{Name: "email", Type: "text", Nullable: true},
	}))
}

// 1. old-only -> old+new -> new-only
func TestBuild_OldOnlyCoexistNewOnly(t *testing.T) {
	w := mustWorkload(t, rollingUpdateStrategy())
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: mustSchema(t),
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(g.Nodes) != 3 {
		t.Fatalf("expected 3 nodes (old, coexist, new), got %d: %+v", len(g.Nodes), g.Nodes)
	}
	start := g.State(g.Start)
	if len(start.Live) != 1 || start.Live[0].Version != "v1" {
		t.Fatalf("expected start state to have only v1 live, got %+v", start.Live)
	}
	target := g.State(g.Target)
	if len(target.Live) != 1 || target.Live[0].Version != "v2" {
		t.Fatalf("expected target state to have only v2 live, got %+v", target.Live)
	}
	foundCoexist := false
	for _, n := range g.Nodes {
		if len(n.Live) == 2 {
			foundCoexist = true
		}
	}
	if !foundCoexist {
		t.Fatalf("expected a coexistence node to be reachable")
	}
}

// 2. migration commits during rollout: the coexistence state must be
// reachable with the migration already applied (the flagship hazard).
func TestBuild_MigrationDuringRollout_CoexistenceWithAppliedSchemaReachable(t *testing.T) {
	w := mustWorkload(t, rollingUpdateStrategy())
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: baseSchemaWithEmail(t),
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: dropEmailMigration(t), Phase: ir.PhaseDuringRollout}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	emailRef := ir.ColumnRef{Table: "users", Column: "email"}
	foundHazard := false
	for _, n := range g.Nodes {
		if len(n.Live) == 2 && !n.SchemaState.Schema.HasColumn(emailRef) {
			foundHazard = true
		}
	}
	if !foundHazard {
		t.Fatalf("expected a coexistence node with the migration already applied to be reachable")
	}

	// Also confirm the pre-migration coexistence state is reachable
	// (ordering is genuinely ambiguous — both must exist).
	foundPending := false
	for _, n := range g.Nodes {
		if len(n.Live) == 2 && n.SchemaState.Schema.HasColumn(emailRef) {
			foundPending = true
		}
	}
	if !foundPending {
		t.Fatalf("expected a coexistence node with the migration still pending to be reachable")
	}

	if g.State(g.Target).SchemaState.Schema.HasColumn(emailRef) {
		t.Fatalf("sanity: target schema should not have the dropped column")
	}
}

// 3. migration after rollout: the fully-new state must be reachable both
// with the migration pending and with it applied (SC-UNSAFE-004/005).
func TestBuild_MigrationAfterRollout_PendingStateReachable(t *testing.T) {
	w := mustWorkload(t, rollingUpdateStrategy())
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: baseSchemaWithEmail(t),
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: dropEmailMigration(t), Phase: ir.PhaseAfterRollout}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	emailRef := ir.ColumnRef{Table: "users", Column: "email"}
	foundPendingNewOnly := false
	for _, n := range g.Nodes {
		if len(n.Live) == 1 && n.Live[0].Version == "v2" && n.SchemaState.Schema.HasColumn(emailRef) {
			foundPendingNewOnly = true
		}
	}
	if !foundPendingNewOnly {
		t.Fatalf("expected a new-only state with the migration still pending to be reachable")
	}
	if g.State(g.Target).SchemaState.Schema.HasColumn(emailRef) {
		t.Fatalf("expected the final target state to have the migration applied")
	}
}

// 4. Recreate must never allow a coexistence node.
func TestBuild_Recreate_NoCoexistence(t *testing.T) {
	w := mustWorkload(t, ir.RolloutStrategy{Type: ir.StrategyRecreate})
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: mustSchema(t),
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, n := range g.Nodes {
		if len(n.Live) == 2 {
			t.Fatalf("Recreate strategy must never produce a coexistence node, found: %+v", n)
		}
	}
}

// 4b. A RollingUpdate configured with no possible unavailability window
// must also avoid coexistence.
func TestBuild_RollingUpdateWithNoSurgeOrUnavailability_NoCoexistence(t *testing.T) {
	w := mustWorkload(t, noCoexistenceRollingStrategy())
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: mustSchema(t),
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, n := range g.Nodes {
		if len(n.Live) == 2 {
			t.Fatalf("expected no coexistence node when maxUnavailable == replicas, found: %+v", n)
		}
	}
}

// 5. Partial rollout: an unclassified migration operation must mark
// downstream states Indeterminate, never silently treated as applied.
func TestBuild_UnclassifiedOperation_MarksStatesIndeterminate(t *testing.T) {
	w := mustWorkload(t, rollingUpdateStrategy())
	badMigration, err := ir.NewMigration("099_weird.sql", []ir.MigrationOp{
		{Kind: ir.OpUnclassified, RawStatement: "CREATE TRIGGER t ..."},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: mustSchema(t),
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: badMigration, Phase: ir.PhaseDuringRollout}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	foundIndeterminate := false
	for _, n := range g.Nodes {
		if n.SchemaState.Indeterminate {
			foundIndeterminate = true
		}
	}
	if !foundIndeterminate {
		t.Fatalf("expected at least one state to be marked Indeterminate")
	}
	if g.State(g.Start).SchemaState.Indeterminate {
		t.Fatalf("the start state (nothing committed yet) must not be Indeterminate")
	}
}

// 6. Rollback is modeled as its own graph, built from the current
// (already-migrated, un-reverted) schema — not a reversal of the forward
// graph (docs/architecture.md §5).
func TestBuild_RollbackGraph_EvaluatedAgainstCurrentSchema(t *testing.T) {
	w := mustWorkload(t, rollingUpdateStrategy())
	forwardPlan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: baseSchemaWithEmail(t),
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: dropEmailMigration(t), Phase: ir.PhaseBeforeRollout}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	forwardGraph, err := Build(forwardPlan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	currentSchema := forwardGraph.State(forwardGraph.Target).SchemaState.Schema

	rollbackWorkload, err := ir.NewWorkload(ir.Workload{
		Name: "api", ServiceName: "api", Version: "v1", Replicas: 3, Strategy: rollingUpdateStrategy(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rollbackPlan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: currentSchema, // no migration reversal — the schema stays as-is
		Workloads:  []ir.WorkloadChange{{Workload: rollbackWorkload, FromVersion: "v2", ToVersion: "v1"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rollbackGraph, err := Build(rollbackPlan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	emailRef := ir.ColumnRef{Table: "users", Column: "email"}
	foundOldLiveAgainstMigratedSchema := false
	for _, n := range rollbackGraph.Nodes {
		for _, lv := range n.Live {
			if lv.Version == "v1" && !n.SchemaState.Schema.HasColumn(emailRef) {
				foundOldLiveAgainstMigratedSchema = true
			}
		}
	}
	if !foundOldLiveAgainstMigratedSchema {
		t.Fatalf("expected the rollback graph to reach a state where v1 is live against the already-migrated schema")
	}
}

// 7. Impossible states are not generated: node count matches the exact
// generative formula, never a superset padded with unreachable combos.
func TestBuild_NoImpossibleStatesGenerated(t *testing.T) {
	w := mustWorkload(t, rollingUpdateStrategy())
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: baseSchemaWithEmail(t),
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: dropEmailMigration(t), Phase: ir.PhaseDuringRollout}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 1 workload x 3 progression steps x 2^1 duringVectors = 6 grid
	// nodes; no afterMigs, so no additional chained nodes.
	if len(g.Nodes) != 6 {
		t.Fatalf("expected exactly 6 nodes, got %d: %+v", len(g.Nodes), g.Nodes)
	}
}

// 8. Same semantic input produces an identical graph.
func TestBuild_Deterministic(t *testing.T) {
	build := func() *Graph {
		w := mustWorkload(t, rollingUpdateStrategy())
		plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
			BaseSchema: baseSchemaWithEmail(t),
			Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
			Migrations: []ir.MigrationTiming{{Migration: dropEmailMigration(t), Phase: ir.PhaseDuringRollout}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		g, err := Build(plan)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return g
	}
	a := build()
	b := build()
	if !reflect.DeepEqual(a.Nodes, b.Nodes) {
		t.Fatalf("nondeterministic node construction")
	}
	if !reflect.DeepEqual(a.Edges, b.Edges) {
		t.Fatalf("nondeterministic edge construction")
	}
	if a.Start != b.Start || a.Target != b.Target {
		t.Fatalf("nondeterministic start/target selection")
	}
}

func TestShortestPath_FlagshipHazardIsTwoEvents(t *testing.T) {
	w := mustWorkload(t, rollingUpdateStrategy())
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: baseSchemaWithEmail(t),
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: dropEmailMigration(t), Phase: ir.PhaseDuringRollout}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	emailRef := ir.ColumnRef{Table: "users", Column: "email"}
	var hazardNode = -1
	for _, n := range g.Nodes {
		if len(n.Live) == 2 && !n.SchemaState.Schema.HasColumn(emailRef) {
			hazardNode = n.ID
			break
		}
	}
	if hazardNode == -1 {
		t.Fatalf("expected to find the hazard node")
	}
	path := g.ShortestPath(hazardNode)
	if len(path) != 2 {
		t.Fatalf("expected the shortest path to the flagship hazard to have 2 events, got %d: %+v", len(path), path)
	}
}

func TestShortestPath_ToStartIsEmpty(t *testing.T) {
	w := mustWorkload(t, rollingUpdateStrategy())
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: mustSchema(t),
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path := g.ShortestPath(g.Start); len(path) != 0 {
		t.Fatalf("expected empty path to the start node, got %+v", path)
	}
}

// TestBuild_RejectsOversizedPlan is the regression for an adversarial-review
// finding: before maxGraphNodes existed, a plan with enough
// PhaseDuringRollout migrations (2^D nodes) had no bound at all and could
// exhaust memory or hang instead of failing fast with a clear error.
func TestBuild_RejectsOversizedPlan(t *testing.T) {
	w := mustWorkload(t, rollingUpdateStrategy())
	// 20 PhaseDuringRollout migrations -> 2^20 (over 1,000,000) nodes,
	// comfortably past maxGraphNodes.
	migrations := make([]ir.MigrationTiming, 0, 20)
	for i := 0; i < 20; i++ {
		m, err := ir.NewMigration(fmt.Sprintf("m%d.sql", i), []ir.MigrationOp{
			{Kind: ir.OpAddColumn, Table: "t", Column: fmt.Sprintf("c%d", i), NewType: "text", Nullable: true},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		migrations = append(migrations, ir.MigrationTiming{Migration: m, Phase: ir.PhaseDuringRollout})
	}
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: mustSchema(t, mustTable(t, "t", nil)),
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations: migrations,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := Build(plan); err == nil {
		t.Fatalf("expected Build to reject a plan whose graph would exceed maxGraphNodes")
	}
}

// State-space completeness attack: with two workloads BOTH genuinely
// transitioning (FromVersion != ToVersion, both RollingUpdate with
// coexistence) at once, the graph must contain every point in the full
// 3x3 progression lattice — including the "worst case" mid-point where
// BOTH services have old and new simultaneously live (four LiveVersion
// entries at once) — not just the corners or a single workload's own
// progression with the other silently held fixed. Every graph test
// elsewhere in this package exercises exactly one transitioning workload
// (plus, elsewhere in the invariant package's own tests, one *static*
// non-transitioning dependency workload) — this is the one place two
// independently progressing workloads are combined, which is exactly
// where an interleaving could be missed if buildVersionCombos's cartesian
// product were ever narrowed to "advance one workload fully before the
// other starts" instead of the full product it is by construction.
func TestBuild_TwoSimultaneousWorkloadTransitions_FullLatticeReachable(t *testing.T) {
	front, err := ir.NewWorkload(ir.Workload{Name: "front", ServiceName: "front", Version: "v2", Replicas: 3, Strategy: rollingUpdateStrategy()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	back, err := ir.NewWorkload(ir.Workload{Name: "back", ServiceName: "back", Version: "v2", Replicas: 3, Strategy: rollingUpdateStrategy()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: mustSchema(t, mustTable(t, "t", []ir.Column{{Name: "id", Type: "integer"}})),
		Workloads: []ir.WorkloadChange{
			{Workload: front, FromVersion: "v1", ToVersion: "v2"},
			{Workload: back, FromVersion: "v1", ToVersion: "v2"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 2 workloads x 3 progression steps each x 2^0 duringVectors (no
	// migrations) = 9 nodes exactly — no more (impossible states), no
	// fewer (omitted interleavings).
	if len(g.Nodes) != 9 {
		t.Fatalf("expected exactly 9 nodes (3x3 lattice), got %d: %+v", len(g.Nodes), g.Nodes)
	}

	frontVersions := func(live []ir.LiveVersion) map[string]bool {
		out := map[string]bool{}
		for _, lv := range live {
			if lv.ServiceName == "front" {
				out[lv.Version] = true
			}
		}
		return out
	}
	backVersions := func(live []ir.LiveVersion) map[string]bool {
		out := map[string]bool{}
		for _, lv := range live {
			if lv.ServiceName == "back" {
				out[lv.Version] = true
			}
		}
		return out
	}

	// Every one of the 3x3 = 9 combinations of {front step} x {back
	// step} must be present among the reachable states, including the
	// center point where BOTH are simultaneously coexisting.
	want := []struct{ front, back []string }{
		{[]string{"v1"}, []string{"v1"}},
		{[]string{"v1"}, []string{"v1", "v2"}},
		{[]string{"v1"}, []string{"v2"}},
		{[]string{"v1", "v2"}, []string{"v1"}},
		{[]string{"v1", "v2"}, []string{"v1", "v2"}}, // the four-versions-live worst case
		{[]string{"v1", "v2"}, []string{"v2"}},
		{[]string{"v2"}, []string{"v1"}},
		{[]string{"v2"}, []string{"v1", "v2"}},
		{[]string{"v2"}, []string{"v2"}},
	}
	for _, w := range want {
		found := false
		for _, n := range g.Nodes {
			fv, bv := frontVersions(n.Live), backVersions(n.Live)
			if len(fv) != len(w.front) || len(bv) != len(w.back) {
				continue
			}
			match := true
			for _, v := range w.front {
				match = match && fv[v]
			}
			for _, v := range w.back {
				match = match && bv[v]
			}
			if match {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected a reachable state with front=%v, back=%v, found none among %d nodes", w.front, w.back, len(g.Nodes))
		}
	}

	// No node may show a "front" version-set that never legitimately
	// appears in front's own progression (e.g. an empty set, which
	// AllowsCoexistence's RollingUpdate progression never produces) —
	// the impossible-state side of the same completeness property.
	for _, n := range g.Nodes {
		if len(frontVersions(n.Live)) == 0 || len(backVersions(n.Live)) == 0 {
			t.Errorf("node %d has an empty version set for a coexistence-capable workload, which should never be reachable: %+v", n.ID, n.Live)
		}
	}
}

func TestBuild_RejectsPhaseUnknown(t *testing.T) {
	// ir.NewRolloutPlan already rejects PhaseUnknown, but Build must also
	// defend itself if ever called directly with an unvalidated plan.
	w := mustWorkload(t, rollingUpdateStrategy())
	badMigration, _ := ir.NewMigration("x.sql", nil)
	plan := ir.RolloutPlan{
		BaseSchema: mustSchema(t),
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: badMigration, Phase: ir.PhaseUnknown}},
	}
	if _, err := Build(plan); err == nil {
		t.Fatalf("expected Build to reject a migration with PhaseUnknown")
	}
}
