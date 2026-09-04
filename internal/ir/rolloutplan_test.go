package ir

import "testing"

func validWorkload(t *testing.T) Workload {
	t.Helper()
	w, err := NewWorkload(Workload{
		Name:        "api",
		ServiceName: "api",
		Version:     "v2",
		Replicas:    3,
		Strategy:    RolloutStrategy{Type: StrategyRollingUpdate, MaxSurge: IntOrPercent{IntValue: 1}},
	})
	if err != nil {
		t.Fatalf("unexpected error building workload: %v", err)
	}
	return w
}

func TestNewRolloutPlan_RejectsNoWorkloads(t *testing.T) {
	base, err := NewSchema(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := NewRolloutPlan(RolloutPlan{BaseSchema: base}); err == nil {
		t.Fatalf("expected error for a plan with no workload changes")
	}
}

func TestNewRolloutPlan_RejectsMissingPhase(t *testing.T) {
	base, _ := NewSchema(nil)
	w := validWorkload(t)
	mig, err := NewMigration("001.sql", []MigrationOp{{Kind: OpAddColumn, Table: "users", Column: "x", NewType: "text", Nullable: true}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = NewRolloutPlan(RolloutPlan{
		BaseSchema: base,
		Workloads:  []WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []MigrationTiming{{Migration: mig, Phase: PhaseUnknown}},
	})
	if err == nil {
		t.Fatalf("expected error when a migration has PhaseUnknown")
	}
}

func TestNewRolloutPlan_RejectsWorkloadChangeMissingVersions(t *testing.T) {
	base, _ := NewSchema(nil)
	w := validWorkload(t)
	_, err := NewRolloutPlan(RolloutPlan{
		BaseSchema: base,
		Workloads:  []WorkloadChange{{Workload: w, FromVersion: "", ToVersion: "v2"}},
	})
	if err == nil {
		t.Fatalf("expected error for missing FromVersion")
	}
}

func TestNewRolloutPlan_Valid(t *testing.T) {
	base, _ := NewSchema(nil)
	w := validWorkload(t)
	mig, _ := NewMigration("001.sql", []MigrationOp{{Kind: OpAddColumn, Table: "users", Column: "x", NewType: "text", Nullable: true}})
	plan, err := NewRolloutPlan(RolloutPlan{
		BaseSchema: base,
		Workloads:  []WorkloadChange{{Workload: w, FromVersion: "v1", ToVersion: "v2"}},
		Migrations: []MigrationTiming{{Migration: mig, Phase: PhaseDuringRollout}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Workloads) != 1 || len(plan.Migrations) != 1 {
		t.Fatalf("unexpected plan shape: %+v", plan)
	}
}

func TestRolloutStrategy_AllowsCoexistence(t *testing.T) {
	cases := []struct {
		name     string
		strategy RolloutStrategy
		replicas int
		want     bool
	}{
		{"recreate never coexists", RolloutStrategy{Type: StrategyRecreate}, 3, false},
		{"unknown strategy treated as no-coexistence-guarantee", RolloutStrategy{Type: StrategyUnknown}, 3, false},
		{"rolling update with surge", RolloutStrategy{Type: StrategyRollingUpdate, MaxSurge: IntOrPercent{IntValue: 1}}, 3, true},
		{"rolling update with unavailable < replicas", RolloutStrategy{Type: StrategyRollingUpdate, MaxUnavailable: IntOrPercent{IntValue: 1}}, 3, true},
		{"rolling update with unavailable == replicas and no surge", RolloutStrategy{Type: StrategyRollingUpdate, MaxUnavailable: IntOrPercent{IntValue: 3}}, 3, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.strategy.AllowsCoexistence(c.replicas); got != c.want {
				t.Fatalf("AllowsCoexistence() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestIntOrPercent_Resolve(t *testing.T) {
	if got := (IntOrPercent{IntValue: 2}).Resolve(10); got != 2 {
		t.Fatalf("expected literal int to resolve to itself, got %d", got)
	}
	if got := (IntOrPercent{IsPercent: true, Percent: 25}).Resolve(10); got != 3 {
		t.Fatalf("expected 25%% of 10 rounded up to be 3, got %d", got)
	}
	if got := (IntOrPercent{IsPercent: true, Percent: 0}).Resolve(10); got != 0 {
		t.Fatalf("expected 0%% to resolve to 0, got %d", got)
	}
}

func TestNewWorkload_RejectsNegativeReplicas(t *testing.T) {
	_, err := NewWorkload(Workload{Name: "api", ServiceName: "api", Version: "v1", Replicas: -1})
	if err == nil {
		t.Fatalf("expected error for negative replicas")
	}
}

func TestNewWorkload_DefaultsKindToDeployment(t *testing.T) {
	w, err := NewWorkload(Workload{Name: "api", ServiceName: "api", Version: "v1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.Kind != "Deployment" {
		t.Fatalf("expected default kind Deployment, got %q", w.Kind)
	}
}
