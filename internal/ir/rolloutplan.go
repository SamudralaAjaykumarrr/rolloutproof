package ir

import "fmt"

// MigrationPhase anchors a migration's commit point relative to a
// workload rollout's progress (docs/architecture.md §2.6, §3.2 step 3).
// The zero value is PhaseUnknown: a migration with no declared phase must
// never be silently treated as the safest option
// (PhaseAfterRollout) — see docs/adr/0008.
type MigrationPhase int

const (
	PhaseUnknown MigrationPhase = iota
	PhaseBeforeRollout
	PhaseDuringRollout
	PhaseAfterRollout
)

func (p MigrationPhase) String() string {
	switch p {
	case PhaseBeforeRollout:
		return "PhaseBeforeRollout"
	case PhaseDuringRollout:
		return "PhaseDuringRollout"
	case PhaseAfterRollout:
		return "PhaseAfterRollout"
	default:
		return "PhaseUnknown"
	}
}

// WorkloadChange names one workload's version transition within a
// RolloutPlan.
type WorkloadChange struct {
	Workload    Workload
	FromVersion string
	ToVersion   string
}

// MigrationTiming pairs a Migration with the rollout phase it is
// declared to commit in.
type MigrationTiming struct {
	Migration Migration
	Phase     MigrationPhase
}

// RollbackTarget names a workload version to revert to. Whether that
// reversion is safe is evaluated by internal/invariant against the
// RolloutPlan's schema state at the point of rollback
// (docs/architecture.md §5), not decided here.
type RollbackTarget struct {
	Workload  Workload
	ToVersion string
}

// RolloutPlan is the top-level unit the transition graph is built from:
// what changes, when migrations commit relative to that change, and what
// the schema looked like before any of it started.
type RolloutPlan struct {
	BaseSchema     Schema
	Workloads      []WorkloadChange
	Migrations     []MigrationTiming
	RollbackTarget *RollbackTarget
}

func (p RolloutPlan) validate() error {
	if len(p.Workloads) == 0 {
		return fmt.Errorf("ir: rollout plan must declare at least one workload change")
	}
	for _, w := range p.Workloads {
		if w.FromVersion == "" || w.ToVersion == "" {
			return fmt.Errorf("ir: workload %q: FromVersion and ToVersion must both be set", w.Workload.Name)
		}
	}
	for _, m := range p.Migrations {
		if m.Phase == PhaseUnknown {
			return fmt.Errorf("ir: migration %q: phase must be declared explicitly, not left unknown", m.Migration.ID)
		}
	}
	return nil
}

// NewRolloutPlan validates and constructs a RolloutPlan.
func NewRolloutPlan(p RolloutPlan) (RolloutPlan, error) {
	if err := p.validate(); err != nil {
		return RolloutPlan{}, err
	}
	out := p
	out.Workloads = append([]WorkloadChange(nil), p.Workloads...)
	out.Migrations = append([]MigrationTiming(nil), p.Migrations...)
	return out, nil
}
