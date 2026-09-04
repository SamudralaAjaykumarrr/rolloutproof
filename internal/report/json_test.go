package report

import (
	"encoding/json"
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/invariant"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

func unsafeDiags(t *testing.T) []ir.Diagnostic {
	t.Helper()
	idDefault := "nextval('id_seq'::regclass)"
	migration, err := ir.NewMigration("017_drop_email.sql", []ir.MigrationOp{
		{Kind: ir.OpDropColumn, Table: "users", Column: "email", Destructiveness: ir.Destructive, Reversibility: ir.Irreversible, SourceLine: 1},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	schema := mustSchema(t, mustTable(t, "users", []ir.Column{
		{Name: "id", Type: "integer", Default: &idDefault},
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
	return invariant.Evaluate(plan, g, services)
}

func TestRenderJSON_ValidAndDeterministic(t *testing.T) {
	diags := unsafeDiags(t)

	a, err := RenderJSON(diags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, err := RenderJSON(diags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(a) != string(b) {
		t.Fatalf("RenderJSON must be deterministic across repeated calls")
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(a, &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if parsed["verdict"] != "UNSAFE" {
		t.Fatalf("expected verdict UNSAFE, got %v", parsed["verdict"])
	}
	if parsed["schemaVersion"] != JSONSchemaVersion {
		t.Fatalf("expected schemaVersion %q, got %v", JSONSchemaVersion, parsed["schemaVersion"])
	}
	invariants, ok := parsed["invariants"].([]interface{})
	if !ok || len(invariants) == 0 {
		t.Fatalf("expected a non-empty invariants array, got %v", parsed["invariants"])
	}
}

func TestRenderJSON_UnsafeInvariantHasCounterexample(t *testing.T) {
	diags := unsafeDiags(t)
	out, err := RenderJSON(diags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var rep struct {
		Invariants []struct {
			ID             string `json:"id"`
			Verdict        string `json:"verdict"`
			Counterexample *struct {
				Outcome string `json:"outcome"`
			} `json:"counterexample"`
		} `json:"invariants"`
	}
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var found bool
	for _, inv := range rep.Invariants {
		if inv.ID != invariant.RPDB001 {
			continue
		}
		found = true
		if inv.Verdict != "UNSAFE" {
			t.Fatalf("expected RP-DB-001 UNSAFE, got %s", inv.Verdict)
		}
		if inv.Counterexample == nil || inv.Counterexample.Outcome == "" {
			t.Fatalf("expected a non-empty counterexample outcome")
		}
	}
	if !found {
		t.Fatalf("expected an RP-DB-001 entry in the JSON output")
	}
}

func TestRenderJSON_KeyOrderStable(t *testing.T) {
	diags := unsafeDiags(t)
	out, err := RenderJSON(diags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// json.MarshalIndent on a struct (never a map) always emits keys in
	// declaration order — this pins that behavior against a future
	// accidental switch to a map-typed field.
	s := string(out)
	wantOrder := []string{`"schemaVersion"`, `"verdict"`, `"summary"`, `"invariants"`}
	last := -1
	for _, key := range wantOrder {
		idx := indexOf(s, key)
		if idx < 0 {
			t.Fatalf("expected key %s in output", key)
		}
		if idx < last {
			t.Fatalf("expected key %s to appear after the previous key, got out-of-order output:\n%s", key, s)
		}
		last = idx
	}
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
