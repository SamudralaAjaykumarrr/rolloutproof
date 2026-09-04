package ir

import "fmt"

// StrategyType names the rollout mechanism a Workload uses. It determines
// whether old and new replicas can ever be simultaneously live — see
// docs/architecture.md §3.2.
type StrategyType int

const (
	// StrategyUnknown is the zero value: the manifest did not declare a
	// recognized strategy, or the parser could not classify it. Treated
	// as "coexistence cannot be ruled out" wherever it matters, never as
	// Recreate's stronger guarantee.
	StrategyUnknown StrategyType = iota
	StrategyRollingUpdate
	StrategyRecreate
)

func (s StrategyType) String() string {
	switch s {
	case StrategyRollingUpdate:
		return "RollingUpdate"
	case StrategyRecreate:
		return "Recreate"
	default:
		return "Unknown"
	}
}

// IntOrPercent mirrors Kubernetes' IntOrString semantics for
// maxSurge/maxUnavailable without depending on k8s.io/apimachinery from
// this package (ir depends on nothing outside the standard library —
// docs/architecture.md §10). internal/parser/k8s converts from the
// upstream type into this one.
type IntOrPercent struct {
	IsPercent bool
	IntValue  int
	Percent   int
}

// Resolve returns the effective integer value of this IntOrPercent given
// a total replica count, using Kubernetes' own rounding rule for
// percentages (round up for surge-like fields, which is the caller's
// responsibility to apply consistently; Resolve itself just rounds up,
// matching maxSurge's convention — the more common source of this field
// in practice).
func (v IntOrPercent) Resolve(total int) int {
	if !v.IsPercent {
		return v.IntValue
	}
	return (total*v.Percent + 99) / 100
}

func (v IntOrPercent) String() string {
	if v.IsPercent {
		return fmt.Sprintf("%d%%", v.Percent)
	}
	return fmt.Sprintf("%d", v.IntValue)
}

// RolloutStrategy is the subset of a Deployment's rollout strategy that
// determines coexistence reachability.
type RolloutStrategy struct {
	Type           StrategyType
	MaxSurge       IntOrPercent // meaningful only when Type == StrategyRollingUpdate
	MaxUnavailable IntOrPercent // meaningful only when Type == StrategyRollingUpdate
}

// AllowsCoexistence reports whether this strategy can ever produce a
// state where old and new replicas are simultaneously live and serving,
// given the workload's total replica count. Recreate never does;
// RollingUpdate does whenever it can run at least one surge replica
// above the old count, or tolerate fewer than the full old count being
// unavailable (docs/architecture.md §3.2 step 2).
func (s RolloutStrategy) AllowsCoexistence(replicas int) bool {
	if s.Type != StrategyRollingUpdate {
		return false
	}
	surge := s.MaxSurge.Resolve(replicas)
	unavailable := s.MaxUnavailable.Resolve(replicas)
	return surge > 0 || unavailable < replicas
}

// ReadinessSpec captures the readiness facts RolloutProof's V1 invariants
// need. HasReadinessProbe and WaitsOnDependencies are deliberately
// separate fields: the absence of any probe is evidence of nothing about
// dependency-awareness, whereas a positively parsed, non-dependency-aware
// probe is evidence that traffic is NOT gated on dependencies
// (docs/invariants.md RP-K8S-002).
type ReadinessSpec struct {
	HasReadinessProbe   bool
	InitialDelaySeconds int
	WaitsOnDependencies bool
}

// TerminationSpec captures drain-phase facts used by RP-K8S-003.
type TerminationSpec struct {
	GracePeriodSeconds int
	HasPreStopHook     bool
}

// Workload is the normalized form of a Kubernetes Deployment (V1 supports
// no other kind — docs/vision.md §6).
type Workload struct {
	Kind        string // "Deployment" in V1
	Name        string
	Namespace   string
	ServiceName string
	Version     string

	Replicas    int
	Strategy    RolloutStrategy
	Readiness   ReadinessSpec
	Termination TerminationSpec

	DependsOn []string
}

func (w Workload) validate() error {
	if w.Name == "" {
		return fmt.Errorf("ir: workload name must not be empty")
	}
	if w.ServiceName == "" {
		return fmt.Errorf("ir: workload %q: serviceName must not be empty", w.Name)
	}
	if w.Version == "" {
		return fmt.Errorf("ir: workload %q: version must not be empty", w.Name)
	}
	if w.Replicas < 0 {
		return fmt.Errorf("ir: workload %q: replicas must not be negative", w.Name)
	}
	return nil
}

// NewWorkload validates and constructs a Workload.
func NewWorkload(w Workload) (Workload, error) {
	if err := w.validate(); err != nil {
		return Workload{}, err
	}
	if w.Kind == "" {
		w.Kind = "Deployment"
	}
	out := w
	out.DependsOn = append([]string(nil), w.DependsOn...)
	return out, nil
}
