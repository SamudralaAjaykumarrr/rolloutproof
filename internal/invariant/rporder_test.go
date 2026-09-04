package invariant

import (
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
		ok   bool
	}{
		{"v1", "v2", -1, true},
		{"v2", "v1", 1, true},
		{"v1.2.3", "v1.3.0", -1, true},
		{"v1.2.3", "v1.2.3", 0, true},
		{"build-8841", "build-9012", 0, false},
		{"v1", "build-9012", 0, false},
	}
	for _, tc := range cases {
		got, ok := compareVersions(tc.a, tc.b)
		if ok != tc.ok {
			t.Fatalf("compareVersions(%q, %q) ok = %v, want %v", tc.a, tc.b, ok, tc.ok)
		}
		if ok && got != tc.want {
			t.Fatalf("compareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// checkoutPaymentsGraph is a two-workload plan: payments is the
// "changing" workload (v2 -> v4), checkout is static at whatever version
// the test supplies.
func checkoutPaymentsGraph(t *testing.T, checkoutVersion string) (ir.RolloutPlan, *graph.Graph) {
	t.Helper()
	payments, err := ir.NewWorkload(ir.Workload{
		Name: "payments", ServiceName: "payments", Version: "v4", Replicas: 3,
		Strategy: ir.RolloutStrategy{Type: ir.StrategyRollingUpdate, MaxSurge: ir.IntOrPercent{IntValue: 1}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checkout, err := ir.NewWorkload(ir.Workload{
		Name: "checkout", ServiceName: "checkout", Version: checkoutVersion, Replicas: 1,
		Strategy: ir.RolloutStrategy{Type: ir.StrategyRecreate},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: mustSchema(t),
		Workloads: []ir.WorkloadChange{
			{Workload: payments, FromVersion: "v2", ToVersion: "v4"},
			{Workload: checkout, FromVersion: checkoutVersion, ToVersion: checkoutVersion},
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

// SC-UNSAFE-011-shaped: checkout is only compatible with payments up to
// v2 semantics, but declares (mistakenly, or because it predates the
// v3 requirement) a MinCompatibleVersion of v3 while payments rolls
// straight to v4 — wait, more directly: checkout declares it needs
// payments >= v3, but payments' reachable live versions during this
// rollout include v2 (the starting point) before reaching v4.
func TestRPORDER_UnsafeConsumerLiveBeforeProviderSupport(t *testing.T) {
	plan, g := checkoutPaymentsGraph(t, "v1")
	checkout, err := ir.NewServiceWithAPI("checkout", "v1", nil, nil,
		[]ir.ServiceDependency{{ServiceName: "payments", MinCompatibleVersion: "v3"}}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	services := map[ir.ServiceKey]ir.Service{
		{Name: "checkout", Version: "v1"}: checkout,
		{Name: "payments", Version: "v2"}: mustService(t, "payments", "v2", nil, nil),
		{Name: "payments", Version: "v4"}: mustService(t, "payments", "v4", nil, nil),
	}
	diags := EvaluateOrder(plan, g, services)
	d := diagFor(diags, RPORDER001)
	if d == nil || d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-ORDER-001 UNSAFE, got %+v", d)
	}
	d2 := diagFor(diags, RPORDER002)
	if d2 == nil || d2.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-ORDER-002 UNSAFE (symmetric), got %+v", d2)
	}
	if d.Counterexample == nil || len(d.Evidence) == 0 {
		t.Fatalf("UNSAFE must carry a counterexample and evidence")
	}
}

func TestRPORDER_SafeWhenAlreadyCompatible(t *testing.T) {
	plan, g := checkoutPaymentsGraph(t, "v1")
	checkout, err := ir.NewServiceWithAPI("checkout", "v1", nil, nil,
		[]ir.ServiceDependency{{ServiceName: "payments", MinCompatibleVersion: "v2"}}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	services := map[ir.ServiceKey]ir.Service{
		{Name: "checkout", Version: "v1"}: checkout,
		{Name: "payments", Version: "v2"}: mustService(t, "payments", "v2", nil, nil),
		{Name: "payments", Version: "v4"}: mustService(t, "payments", "v4", nil, nil),
	}
	diags := EvaluateOrder(plan, g, services)
	if got := Aggregate(diags); got != ir.VerdictSafe {
		t.Fatalf("expected SAFE, got %v (%+v)", got, diags)
	}
}

// SC-UNKNOWN-002: opaque-unordered version scheme makes comparison
// impossible.
func TestRPORDER_UnknownForIncomparableVersions(t *testing.T) {
	plan, g := checkoutPaymentsGraph(t, "v1")
	checkout, err := ir.NewServiceWithAPI("checkout", "v1", nil, nil,
		[]ir.ServiceDependency{{ServiceName: "payments", MinCompatibleVersion: "build-8841"}}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	services := map[ir.ServiceKey]ir.Service{
		{Name: "checkout", Version: "v1"}: checkout,
		{Name: "payments", Version: "v2"}: mustService(t, "payments", "v2", nil, nil),
		{Name: "payments", Version: "v4"}: mustService(t, "payments", "v4", nil, nil),
	}
	diags := EvaluateOrder(plan, g, services)
	d := diagFor(diags, RPORDER001)
	if d == nil || d.Verdict != ir.VerdictUnknown || len(d.MissingEvidence) == 0 {
		t.Fatalf("expected RP-ORDER-001 UNKNOWN with identified missing evidence, got %+v", d)
	}
}

func TestRPORDER_NotApplicableWithoutDeclaredDependency(t *testing.T) {
	plan, g := checkoutPaymentsGraph(t, "v1")
	services := map[ir.ServiceKey]ir.Service{
		{Name: "checkout", Version: "v1"}: mustService(t, "checkout", "v1", nil, nil),
		{Name: "payments", Version: "v2"}: mustService(t, "payments", "v2", nil, nil),
		{Name: "payments", Version: "v4"}: mustService(t, "payments", "v4", nil, nil),
	}
	diags := EvaluateOrder(plan, g, services)
	if got := Aggregate(diags); got != ir.VerdictSafe {
		t.Fatalf("expected SAFE (not applicable), got %v (%+v)", got, diags)
	}
}
