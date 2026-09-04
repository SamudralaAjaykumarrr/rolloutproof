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

// ExpandContractLink declares an explicit expand/contract relationship
// between two migrations in this plan (docs/invariants.md RP-DB-006).
// RolloutProof never infers this relationship from column naming or file
// order — only an explicit link like this one, sourced from the
// project's own rolloutplan.yaml (internal/parser/rolloutplan), is
// evidence of intent (docs/vision.md §6).
type ExpandContractLink struct {
	// ExpandMigrationID and ContractMigrationID are Migration.ID values
	// (conventionally source file paths) that must both appear among the
	// plan's own Migrations — a link naming a migration that isn't part
	// of this plan is a construction error, not a runtime UNKNOWN.
	ExpandMigrationID   string
	ContractMigrationID string
}

// RolloutPlan is the top-level unit the transition graph is built from:
// what changes, when migrations commit relative to that change, and what
// the schema looked like before any of it started.
type RolloutPlan struct {
	BaseSchema          Schema
	Workloads           []WorkloadChange
	Migrations          []MigrationTiming
	RollbackTarget      *RollbackTarget
	ExpandContractLinks []ExpandContractLink
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
	migByID := make(map[string]Migration, len(p.Migrations))
	for _, m := range p.Migrations {
		if m.Phase == PhaseUnknown {
			return fmt.Errorf("ir: migration %q: phase must be declared explicitly, not left unknown", m.Migration.ID)
		}
		migByID[m.Migration.ID] = m.Migration
	}
	for _, link := range p.ExpandContractLinks {
		if link.ExpandMigrationID == "" || link.ContractMigrationID == "" {
			return fmt.Errorf("ir: expand/contract link: both migration IDs must be set")
		}
		if link.ExpandMigrationID == link.ContractMigrationID {
			return fmt.Errorf("ir: expand/contract link: expand and contract must name different migrations, both named %q", link.ExpandMigrationID)
		}
		expand, ok := migByID[link.ExpandMigrationID]
		if !ok {
			return fmt.Errorf("ir: expand/contract link: expand migration %q is not part of this plan's Migrations", link.ExpandMigrationID)
		}
		contract, ok := migByID[link.ContractMigrationID]
		if !ok {
			return fmt.Errorf("ir: expand/contract link: contract migration %q is not part of this plan's Migrations", link.ContractMigrationID)
		}
		if !migrationHasOpKind(expand, OpAddColumn) {
			return fmt.Errorf("ir: expand/contract link: expand migration %q declares no OpAddColumn operation", link.ExpandMigrationID)
		}
		if !migrationHasOpKind(contract, OpDropColumn) && !migrationHasOpKind(contract, OpDropNotNull) {
			return fmt.Errorf("ir: expand/contract link: contract migration %q declares no OpDropColumn/OpDropNotNull operation", link.ContractMigrationID)
		}
	}
	return nil
}

func migrationHasOpKind(m Migration, kind OpKind) bool {
	for _, op := range m.Operations {
		if op.Kind == kind {
			return true
		}
	}
	return false
}

// NewRolloutPlan validates and constructs a RolloutPlan.
func NewRolloutPlan(p RolloutPlan) (RolloutPlan, error) {
	if err := p.validate(); err != nil {
		return RolloutPlan{}, err
	}
	out := p
	out.Workloads = append([]WorkloadChange(nil), p.Workloads...)
	out.Migrations = append([]MigrationTiming(nil), p.Migrations...)
	out.ExpandContractLinks = append([]ExpandContractLink(nil), p.ExpandContractLinks...)
	return out, nil
}
