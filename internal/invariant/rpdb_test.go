package invariant

import (
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

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

func dropEmailMigration(t *testing.T) ir.Migration {
	t.Helper()
	m, err := ir.NewMigration("017_drop_email.sql", []ir.MigrationOp{
		{Kind: ir.OpDropColumn, Table: "users", Column: "email", Destructiveness: ir.Destructive, Reversibility: ir.Irreversible, SourceLine: 1},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return m
}

func flagshipGraph(t *testing.T) *graph.Graph {
	t.Helper()
	schema := mustSchema(t, mustTable(t, "users", []ir.Column{
		{Name: "id", Type: "integer"},
		{Name: "email", Type: "text", Nullable: true},
	}))
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: schema,
		Workloads:  []ir.WorkloadChange{{Workload: mustWorkload(t), FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: dropEmailMigration(t), Phase: ir.PhaseDuringRollout}},
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

func mustService(t *testing.T, name, version string, reads, writes []ir.ColumnRef) ir.Service {
	t.Helper()
	s, err := ir.NewService(name, version, reads, writes, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return s
}

// This is the flagship scenario: it must be DERIVED from the graph and
// service facts below, not hard-coded to any scenario name.
func TestRPDB001_FlagshipUnsafe(t *testing.T) {
	g := flagshipGraph(t)
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v1"}: mustService(t, "api", "v1", []ir.ColumnRef{{Table: "users", Column: "email"}}, nil),
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", nil, nil),
	}

	diags := Evaluate(g, services)
	var rpdb001 *ir.Diagnostic
	for i := range diags {
		if diags[i].InvariantID == RPDB001 {
			rpdb001 = &diags[i]
		}
	}
	if rpdb001 == nil {
		t.Fatalf("expected an RP-DB-001 diagnostic")
	}
	if rpdb001.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected UNSAFE, got %v", rpdb001.Verdict)
	}
	if rpdb001.Counterexample == nil {
		t.Fatalf("UNSAFE must always carry a counterexample")
	}
	if len(rpdb001.Evidence) == 0 {
		t.Fatalf("UNSAFE must always carry evidence")
	}
	// The shortest reachable violation is not the two-event "coexistence"
	// path the narrative example in docs/invariants.md walks through for
	// illustration — it is the one-event path where the migration commits
	// while api@v1 is still the *only* live version, before any new
	// replica has even started. PhaseDuringRollout's declared ambiguity
	// (docs/architecture.md §3.3, assumption A2) makes that state
	// reachable too, and it is strictly simpler, so the deterministic
	// shortest-path rule (§4.4) must select it. This is a real, more
	// precise finding than the illustrative example: the underlying
	// hazard is "a live version depends on a column the committed schema
	// lacks," which coexistence merely happens to also produce — it is
	// not the only, or even the shortest, way to reach it.
	if len(rpdb001.Counterexample.Path) != 1 {
		t.Fatalf("expected the shortest counterexample path to have 1 event, got %d: %+v", len(rpdb001.Counterexample.Path), rpdb001.Counterexample.Path)
	}
	if len(rpdb001.Counterexample.ViolatingState.Live) != 1 {
		t.Fatalf("expected the shortest violation to be the old-only state, got live=%+v", rpdb001.Counterexample.ViolatingState.Live)
	}
	if rpdb001.RollbackVerdict != ir.RollbackUnsafe {
		t.Fatalf("expected rollback to be classified UNSAFE (irreversible drop, old version depends on it), got %v", rpdb001.RollbackVerdict)
	}
	if len(rpdb001.Counterexample.RecommendedSequence) == 0 {
		t.Fatalf("expected a recommended remediation sequence")
	}
}

// Removing the old version's dependency must make the same engine report
// SAFE — proving the result is reasoned from facts, not scenario identity
// (the acceptance-demonstration mutation test, section 19 of the run).
func TestRPDB001_SafeWhenDependencyRemoved(t *testing.T) {
	g := flagshipGraph(t)
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v1"}: mustService(t, "api", "v1", []ir.ColumnRef{{Table: "users", Column: "id"}}, nil),
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", nil, nil),
	}
	diags := Evaluate(g, services)
	for _, d := range diags {
		if d.Verdict == ir.VerdictUnsafe {
			t.Fatalf("expected no UNSAFE diagnostics once the dependency is removed, got %+v", d)
		}
	}
}

func TestRPDB002_WriterFlagshipUnsafe(t *testing.T) {
	g := flagshipGraph(t)
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v1"}: mustService(t, "api", "v1", nil, []ir.ColumnRef{{Table: "users", Column: "email"}}),
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", nil, nil),
	}
	diags := Evaluate(g, services)
	var rpdb002 *ir.Diagnostic
	for i := range diags {
		if diags[i].InvariantID == RPDB002 {
			rpdb002 = &diags[i]
		}
	}
	if rpdb002 == nil || rpdb002.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-DB-002 UNSAFE, got %+v", rpdb002)
	}
}

// isDropColumnInvariant reports whether id is one of the invariants that
// can find anything to evaluate against flagshipGraph's plain DropColumn
// migration — RP-DB-004 (NOT NULL) and RP-DB-005 (rename) correctly find
// no relevant op at all in this fixture and report SAFE regardless of
// service metadata, which is vacuous truth, not a missed UNKNOWN.
func isDropColumnInvariant(id string) bool {
	return id == RPDB001 || id == RPDB002
}

func TestRPDB001_UnknownWhenContractMissing(t *testing.T) {
	g := flagshipGraph(t)
	services := map[ir.ServiceKey]ir.Service{
		// api@v1 has no entry at all.
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", nil, nil),
	}
	diags := Evaluate(g, services)
	for _, d := range diags {
		if !isDropColumnInvariant(d.InvariantID) {
			continue
		}
		if d.Verdict != ir.VerdictUnknown {
			t.Fatalf("expected UNKNOWN when a live version's contract is entirely missing, got %v for %s", d.Verdict, d.InvariantID)
		}
		if len(d.MissingEvidence) == 0 {
			t.Fatalf("UNKNOWN must always identify the missing evidence")
		}
	}
}

func TestRPDB001_UnknownWhenTableNotMentioned(t *testing.T) {
	g := flagshipGraph(t)
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v1"}: mustService(t, "api", "v1", nil, nil), // never mentions "users" at all
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", nil, nil),
	}
	diags := Evaluate(g, services)
	for _, d := range diags {
		if !isDropColumnInvariant(d.InvariantID) {
			continue
		}
		if d.Verdict != ir.VerdictUnknown {
			t.Fatalf("expected UNKNOWN when contract metadata never mentions the affected table, got %v", d.Verdict)
		}
	}
}

func TestRPDB001_SafeWhenNoMigrationDropsAnything(t *testing.T) {
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
	diags := Evaluate(g, map[ir.ServiceKey]ir.Service{})
	if Aggregate(diags) != ir.VerdictSafe {
		t.Fatalf("expected overall SAFE when there is no destructive migration, got %v (%+v)", Aggregate(diags), diags)
	}
}

// Mutation test (run instructions §19): switching the rollout strategy
// from RollingUpdate to Recreate removes the coexistence node entirely
// (proven at the graph layer, internal/graph's
// TestBuild_Recreate_NoCoexistence) — but it must NOT, by itself, flip
// this particular hazard to SAFE. RP-DB-001/002 fire whenever *any*
// live version's declared facts conflict with the committed schema in
// *any* reachable state; coexistence is one way to reach such a state in
// the flagship example, not the only one. With PhaseDuringRollout's
// declared ordering ambiguity, "api@v1 alone, migration already
// committed" is reachable regardless of strategy (see
// TestRPDB001_FlagshipUnsafe's own shortest-path finding), so Recreate
// changes the *reachable state set* without changing *this invariant
// pair's verdict* — exactly the "changes only if justified" property the
// run instructions ask to verify, and here the honest answer is that it
// is not justified to change.
func TestRPDB001_RecreateChangesReachabilityNotThisVerdict(t *testing.T) {
	w, err := ir.NewWorkload(ir.Workload{
		Name: "api", ServiceName: "api", Version: "v2", Replicas: 3,
		Strategy: ir.RolloutStrategy{Type: ir.StrategyRecreate},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	schema := mustSchema(t, mustTable(t, "users", []ir.Column{{Name: "email", Type: "text", Nullable: true}}))
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: schema,
		Workloads:  []ir.WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []ir.MigrationTiming{{Migration: dropEmailMigration(t), Phase: ir.PhaseDuringRollout}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, err := graph.Build(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, n := range g.Nodes {
		if len(n.Live) == 2 {
			t.Fatalf("sanity: Recreate must not produce a coexistence node, found %+v", n)
		}
	}
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v1"}: mustService(t, "api", "v1", []ir.ColumnRef{{Table: "users", Column: "email"}}, nil),
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", nil, nil),
	}
	diags := Evaluate(g, services)
	if Aggregate(diags) != ir.VerdictUnsafe {
		t.Fatalf("expected UNSAFE to persist under Recreate, since the hazard does not depend on coexistence: %+v", diags)
	}
}

func TestAggregate_DominanceOrder(t *testing.T) {
	safe := ir.Diagnostic{Verdict: ir.VerdictSafe}
	unknown := ir.Diagnostic{Verdict: ir.VerdictUnknown}
	unsafe := ir.Diagnostic{Verdict: ir.VerdictUnsafe}

	if got := Aggregate([]ir.Diagnostic{safe, safe}); got != ir.VerdictSafe {
		t.Fatalf("expected SAFE, got %v", got)
	}
	if got := Aggregate([]ir.Diagnostic{safe, unknown}); got != ir.VerdictUnknown {
		t.Fatalf("expected UNKNOWN, got %v", got)
	}
	if got := Aggregate([]ir.Diagnostic{unknown, unsafe}); got != ir.VerdictUnsafe {
		t.Fatalf("expected UNSAFE, got %v", got)
	}
}

func TestEvaluate_Deterministic(t *testing.T) {
	g := flagshipGraph(t)
	services := map[ir.ServiceKey]ir.Service{
		{Name: "api", Version: "v1"}: mustService(t, "api", "v1", []ir.ColumnRef{{Table: "users", Column: "email"}}, nil),
		{Name: "api", Version: "v2"}: mustService(t, "api", "v2", nil, nil),
	}
	a := Evaluate(g, services)
	b := Evaluate(g, services)
	if len(a) != len(b) {
		t.Fatalf("nondeterministic diagnostic count")
	}
	for i := range a {
		if a[i].Verdict != b[i].Verdict || a[i].Summary != b[i].Summary {
			t.Fatalf("nondeterministic diagnostic at index %d: %+v vs %+v", i, a[i], b[i])
		}
	}
}
