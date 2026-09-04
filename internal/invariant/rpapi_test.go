package invariant

import (
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

func mustShape(t *testing.T, fields ...ir.Field) ir.Shape {
	t.Helper()
	s, err := ir.NewShape(fields)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return s
}

func mustAPIContract(t *testing.T, name, service, version string, endpoints ...ir.Endpoint) ir.APIContract {
	t.Helper()
	c, err := ir.NewAPIContract(name, service, version, endpoints)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return c
}

// ordersRolloutGraph is a RollingUpdate coexistence graph for a
// "provider" service change with no schema/migration involved — the
// pure API-compatibility scenarios (SC-SAFE-003, SC-UNSAFE-006/007) all
// share this shape. checkout is modeled as a second, unchanging
// WorkloadChange (FromVersion == ToVersion) purely so it appears in
// every reachable state's Live set at all — RP-API needs the consumer to
// be live in the graph, and a consumer this plan doesn't itself change
// would otherwise never appear there (docs/architecture.md §3's
// RolloutState only tracks declared WorkloadChanges).
func ordersRolloutGraph(t *testing.T) (ir.RolloutPlan, *graph.Graph) {
	t.Helper()
	orders, err := ir.NewWorkload(ir.Workload{
		Name: "orders", ServiceName: "orders", Version: "v2", Replicas: 3,
		Strategy: ir.RolloutStrategy{Type: ir.StrategyRollingUpdate, MaxSurge: ir.IntOrPercent{IntValue: 1}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checkout, err := ir.NewWorkload(ir.Workload{
		Name: "checkout", ServiceName: "checkout", Version: "v1", Replicas: 1,
		Strategy: ir.RolloutStrategy{Type: ir.StrategyRecreate},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: mustSchema(t),
		Workloads: []ir.WorkloadChange{
			{Workload: orders, FromVersion: "v1", ToVersion: "v2"},
			{Workload: checkout, FromVersion: "v1", ToVersion: "v1"},
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

// SC-SAFE-003: provider adds an optional response field; no consumer
// requirement is violated.
func TestRPAPI_SafeBackwardCompatibleAddition(t *testing.T) {
	_, g := ordersRolloutGraph(t)
	checkout, err := ir.NewServiceWithAPI("checkout", "v1", nil, nil, nil, nil,
		[]ir.APIConsumption{{ContractName: "orders-api", Operation: "GET /orders/:id", RequiredResponseFields: []string{"id", "total"}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ordersV1 := mustAPIContract(t, "orders-api", "orders", "v1",
		ir.Endpoint{Operation: "GET /orders/:id", ResponseShape: mustShape(t, ir.Field{Name: "id", Required: true}, ir.Field{Name: "total", Required: true})})
	ordersV2 := mustAPIContract(t, "orders-api", "orders", "v2",
		ir.Endpoint{Operation: "GET /orders/:id", ResponseShape: mustShape(t, ir.Field{Name: "id", Required: true}, ir.Field{Name: "total", Required: true}, ir.Field{Name: "discount_code", Required: false})})

	services := map[ir.ServiceKey]ir.Service{
		{Name: "checkout", Version: "v1"}: checkout,
		{Name: "orders", Version: "v1"}:   mustService(t, "orders", "v1", nil, nil),
		{Name: "orders", Version: "v2"}:   mustService(t, "orders", "v2", nil, nil),
	}
	contracts := map[ir.APIContractKey]ir.APIContract{ordersV1.Key(): ordersV1, ordersV2.Key(): ordersV2}

	diags := EvaluateAPI(g, services, contracts)
	if got := Aggregate(diags); got != ir.VerdictSafe {
		t.Fatalf("expected SAFE, got %v (%+v)", got, diags)
	}
}

// SC-UNSAFE-006: provider renames a required response field while a live
// consumer still requires the old name.
func TestRPAPI003_UnsafeRemovedRequiredResponseField(t *testing.T) {
	_, g := ordersRolloutGraph(t)
	checkout, err := ir.NewServiceWithAPI("checkout", "v1", nil, nil, nil, nil,
		[]ir.APIConsumption{{ContractName: "orders-api", Operation: "GET /orders/:id", RequiredResponseFields: []string{"total"}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ordersV1 := mustAPIContract(t, "orders-api", "orders", "v1",
		ir.Endpoint{Operation: "GET /orders/:id", ResponseShape: mustShape(t, ir.Field{Name: "total", Required: true})})
	ordersV2 := mustAPIContract(t, "orders-api", "orders", "v2",
		ir.Endpoint{Operation: "GET /orders/:id", ResponseShape: mustShape(t, ir.Field{Name: "total_amount", Required: true})})

	services := map[ir.ServiceKey]ir.Service{
		{Name: "checkout", Version: "v1"}: checkout,
		{Name: "orders", Version: "v1"}:   mustService(t, "orders", "v1", nil, nil),
		{Name: "orders", Version: "v2"}:   mustService(t, "orders", "v2", nil, nil),
	}
	contracts := map[ir.APIContractKey]ir.APIContract{ordersV1.Key(): ordersV1, ordersV2.Key(): ordersV2}

	diags := EvaluateAPI(g, services, contracts)
	d := diagFor(diags, RPAPI003)
	if d == nil || d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-API-003 UNSAFE, got %+v", d)
	}
	if d.Counterexample == nil || len(d.Evidence) == 0 {
		t.Fatalf("UNSAFE must carry a counterexample and evidence")
	}
	agg := diagFor(diags, RPAPI004)
	if agg == nil || agg.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-API-004 aggregate UNSAFE, got %+v", agg)
	}
}

// SC-UNSAFE-007: provider removes an endpoint a live consumer still calls.
func TestRPAPI001_UnsafeRemovedEndpoint(t *testing.T) {
	_, g := ordersRolloutGraph(t)
	checkout, err := ir.NewServiceWithAPI("checkout", "v1", nil, nil, nil, nil,
		[]ir.APIConsumption{{ContractName: "orders-api", Operation: "DELETE /orders/:id"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ordersV1 := mustAPIContract(t, "orders-api", "orders", "v1",
		ir.Endpoint{Operation: "DELETE /orders/:id"})
	ordersV2 := mustAPIContract(t, "orders-api", "orders", "v2") // endpoint removed

	services := map[ir.ServiceKey]ir.Service{
		{Name: "checkout", Version: "v1"}: checkout,
		{Name: "orders", Version: "v1"}:   mustService(t, "orders", "v1", nil, nil),
		{Name: "orders", Version: "v2"}:   mustService(t, "orders", "v2", nil, nil),
	}
	contracts := map[ir.APIContractKey]ir.APIContract{ordersV1.Key(): ordersV1, ordersV2.Key(): ordersV2}

	diags := EvaluateAPI(g, services, contracts)
	d := diagFor(diags, RPAPI001)
	if d == nil || d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-API-001 UNSAFE, got %+v", d)
	}
}

// RP-API-002: provider requires a new request field a live consumer
// doesn't send.
func TestRPAPI002_UnsafeNewRequiredRequestField(t *testing.T) {
	_, g := ordersRolloutGraph(t)
	checkout, err := ir.NewServiceWithAPI("checkout", "v1", nil, nil, nil, nil,
		[]ir.APIConsumption{{ContractName: "orders-api", Operation: "POST /orders", RequestFieldsSent: []string{"item_id"}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ordersV1 := mustAPIContract(t, "orders-api", "orders", "v1",
		ir.Endpoint{Operation: "POST /orders", RequestShape: mustShape(t, ir.Field{Name: "item_id", Required: true})})
	ordersV2 := mustAPIContract(t, "orders-api", "orders", "v2",
		ir.Endpoint{Operation: "POST /orders", RequestShape: mustShape(t, ir.Field{Name: "item_id", Required: true}, ir.Field{Name: "warehouse_id", Required: true})})

	services := map[ir.ServiceKey]ir.Service{
		{Name: "checkout", Version: "v1"}: checkout,
		{Name: "orders", Version: "v1"}:   mustService(t, "orders", "v1", nil, nil),
		{Name: "orders", Version: "v2"}:   mustService(t, "orders", "v2", nil, nil),
	}
	contracts := map[ir.APIContractKey]ir.APIContract{ordersV1.Key(): ordersV1, ordersV2.Key(): ordersV2}

	diags := EvaluateAPI(g, services, contracts)
	d := diagFor(diags, RPAPI002)
	if d == nil || d.Verdict != ir.VerdictUnsafe {
		t.Fatalf("expected RP-API-002 UNSAFE, got %+v", d)
	}
}

func TestRPAPI_NotApplicableWithoutAnyDeclaredUsage(t *testing.T) {
	_, g := flagshipGraph(t)
	diags := EvaluateAPI(g, map[ir.ServiceKey]ir.Service{}, map[ir.APIContractKey]ir.APIContract{})
	if got := Aggregate(diags); got != ir.VerdictSafe {
		t.Fatalf("expected SAFE (not applicable), got %v (%+v)", got, diags)
	}
}

// Missing API evidence must report UNKNOWN, never a silent SAFE.
func TestRPAPI_UnknownWhenProviderNotInPlan(t *testing.T) {
	_, g := ordersRolloutGraph(t)
	checkout, err := ir.NewServiceWithAPI("checkout", "v1", nil, nil, nil, nil,
		[]ir.APIConsumption{{ContractName: "orders-api", Operation: "GET /orders/:id", RequiredResponseFields: []string{"total"}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	services := map[ir.ServiceKey]ir.Service{
		{Name: "checkout", Version: "v1"}: checkout,
		{Name: "orders", Version: "v1"}:   mustService(t, "orders", "v1", nil, nil),
		{Name: "orders", Version: "v2"}:   mustService(t, "orders", "v2", nil, nil),
	}
	// No APIContracts registered at all: orders-api's shape is unknown.
	diags := EvaluateAPI(g, services, map[ir.APIContractKey]ir.APIContract{})
	for _, id := range []string{RPAPI001, RPAPI002, RPAPI003} {
		d := diagFor(diags, id)
		if d == nil || d.Verdict != ir.VerdictUnknown || len(d.MissingEvidence) == 0 {
			t.Fatalf("expected %s UNKNOWN with identified missing evidence, got %+v", id, d)
		}
	}

	// Adversarial-review regression: RP-API-004 aggregates
	// RP-API-001/002/003's MissingEvidence, and all three independently
	// discover the *same* gap here (one shared scan, see EvaluateAPI) —
	// the aggregate must deduplicate, not repeat it three times.
	agg := diagFor(diags, RPAPI004)
	if agg == nil || agg.Verdict != ir.VerdictUnknown {
		t.Fatalf("expected RP-API-004 UNKNOWN, got %+v", agg)
	}
	seen := make(map[ir.EvidenceGap]int, len(agg.MissingEvidence))
	for _, g := range agg.MissingEvidence {
		seen[g]++
	}
	for g, count := range seen {
		if count > 1 {
			t.Fatalf("expected RP-API-004's MissingEvidence to be deduplicated, got %s@%s x%d in %+v", g.Field, g.Reason, count, agg.MissingEvidence)
		}
	}
}

// Semantic mutation: removing consumer compatibility (the response field
// it requires disappears) flips SAFE to UNSAFE for the identical
// contract pair otherwise.
func TestRPAPI_MutationRemovingConsumerCompatibilityFlipsToUnsafe(t *testing.T) {
	_, g := ordersRolloutGraph(t)
	ordersV1 := mustAPIContract(t, "orders-api", "orders", "v1",
		ir.Endpoint{Operation: "GET /orders/:id", ResponseShape: mustShape(t, ir.Field{Name: "total", Required: true})})
	ordersV2 := mustAPIContract(t, "orders-api", "orders", "v2",
		ir.Endpoint{Operation: "GET /orders/:id", ResponseShape: mustShape(t, ir.Field{Name: "total", Required: true})})
	contracts := map[ir.APIContractKey]ir.APIContract{ordersV1.Key(): ordersV1, ordersV2.Key(): ordersV2}
	baseServices := map[ir.ServiceKey]ir.Service{
		{Name: "orders", Version: "v1"}: mustService(t, "orders", "v1", nil, nil),
		{Name: "orders", Version: "v2"}: mustService(t, "orders", "v2", nil, nil),
	}

	compatible, err := ir.NewServiceWithAPI("checkout", "v1", nil, nil, nil, nil,
		[]ir.APIConsumption{{ContractName: "orders-api", Operation: "GET /orders/:id", RequiredResponseFields: []string{"total"}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	servicesSafe := map[ir.ServiceKey]ir.Service{{Name: "checkout", Version: "v1"}: compatible}
	for k, v := range baseServices {
		servicesSafe[k] = v
	}
	if got := Aggregate(EvaluateAPI(g, servicesSafe, contracts)); got != ir.VerdictSafe {
		t.Fatalf("expected SAFE for a compatible consumer, got %v", got)
	}

	incompatible, err := ir.NewServiceWithAPI("checkout", "v1", nil, nil, nil, nil,
		[]ir.APIConsumption{{ContractName: "orders-api", Operation: "GET /orders/:id", RequiredResponseFields: []string{"total", "discount_code"}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	servicesUnsafe := map[ir.ServiceKey]ir.Service{{Name: "checkout", Version: "v1"}: incompatible}
	for k, v := range baseServices {
		servicesUnsafe[k] = v
	}
	if got := Aggregate(EvaluateAPI(g, servicesUnsafe, contracts)); got != ir.VerdictUnsafe {
		t.Fatalf("expected UNSAFE once the consumer requires a field the provider never declared, got %v", got)
	}
}
