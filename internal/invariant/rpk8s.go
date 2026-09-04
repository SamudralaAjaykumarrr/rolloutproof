package invariant

import (
	"fmt"
	"sort"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// RPK8S001 is the invariant ID for "rollout permits incompatible versions
// to coexist" (docs/invariants.md).
const RPK8S001 = "RP-K8S-001"

// RPK8S002 is the invariant ID for "readiness admits traffic before
// dependencies are ready" (docs/invariants.md).
const RPK8S002 = "RP-K8S-002"

// RPK8S003 is the invariant ID for "termination/drain assumptions
// conflict with destructive change" (docs/invariants.md).
const RPK8S003 = "RP-K8S-003"

// RPK8S004 is the invariant ID for "rollout parameters create unsafe
// compatibility window" (docs/invariants.md) — a coarse, advisory,
// defense-in-depth check, distinguished in its own Summary text from the
// higher-precision RP-DB findings it may accompany (docs/invariants.md
// RP-K8S-004's own "Limitations").
const RPK8S004 = "RP-K8S-004"

// EvaluateK8s implements the RP-K8S family's highest-value rules
// (docs/invariants.md). allDiagnostics is every other Diagnostic already
// computed for this plan (Evaluate + EvaluateRollbackPlan + EvaluateAPI +
// EvaluateOrder); RP-K8S-001 is the only one that needs it, per its own
// documented mechanism: "a coexistence window can be unsafe even absent
// a specific schema/API conflict RolloutProof can enumerate" is the
// *escape-hatch* case, but V1 has no project-config mechanism yet to
// declare that kind of fact (docs/architecture.md §7's `internal/config`
// is not implemented) — so RP-K8S-001 here fires only on the "derived
// transitively" path docs/invariants.md explicitly allows: another
// invariant already proved incompatibility between two live versions of
// the same service in the same reachable state. This makes RP-K8S-001
// currently a defense-in-depth *echo* of an existing finding, honestly
// documented as such, rather than a fabricated independent capability.
func EvaluateK8s(plan ir.RolloutPlan, g *graph.Graph, allDiagnostics []ir.Diagnostic) []ir.Diagnostic {
	return []ir.Diagnostic{
		evaluateCoexistenceIncompatibility(plan, g, allDiagnostics),
		evaluateReadinessBeforeDependency(plan, g),
		evaluateTerminationConflict(plan, g),
		evaluateUnsafeCompatibilityWindow(plan),
	}
}

// evaluateCoexistenceIncompatibility implements RP-K8S-001. See
// EvaluateK8s's doc comment for V1's "derived transitively only" scoping.
func evaluateCoexistenceIncompatibility(plan ir.RolloutPlan, g *graph.Graph, allDiagnostics []ir.Diagnostic) ir.Diagnostic {
	var derivedFrom *ir.Diagnostic
	for i := range allDiagnostics {
		d := &allDiagnostics[i]
		if d.Verdict != ir.VerdictUnsafe || d.Counterexample == nil {
			continue
		}
		if len(d.Counterexample.ViolatingState.Live) > 1 {
			derivedFrom = d
			break
		}
	}
	if derivedFrom == nil {
		return ir.Diagnostic{
			InvariantID: RPK8S001,
			Verdict:     ir.VerdictSafe,
			Summary:     fmt.Sprintf("%s: no other invariant found a coexisting version pair incompatible (V1 has no separate project-config compatibility declaration — see package doc)", RPK8S001),
		}
	}
	return ir.Diagnostic{
		InvariantID: RPK8S001,
		Verdict:     ir.VerdictUnsafe,
		Summary: fmt.Sprintf(
			"%s: the rollout strategy permits a version pair to coexist that %s independently found incompatible (%s)",
			RPK8S001, derivedFrom.InvariantID, derivedFrom.Summary),
		Evidence: []ir.Evidence{
			{Kind: ir.EvidenceRolloutStrategy, Description: fmt.Sprintf("mechanism-level echo of %s's finding, for defense-in-depth diagnostic clarity (docs/invariants.md RP-K8S-001)", derivedFrom.InvariantID)},
		},
		Counterexample: &ir.Counterexample{
			Path:           derivedFrom.Counterexample.Path,
			ViolatingState: derivedFrom.Counterexample.ViolatingState,
			Outcome:        derivedFrom.Counterexample.Outcome,
		},
	}
}

// evaluateReadinessBeforeDependency implements RP-K8S-002
// (docs/invariants.md): a workload whose readiness does not wait on a
// declared dependency must not have a reachable state where it is live
// at its target version while that dependency is not.
func evaluateReadinessBeforeDependency(plan ir.RolloutPlan, g *graph.Graph) ir.Diagnostic {
	wcByName := make(map[string]ir.WorkloadChange, len(plan.Workloads))
	for _, wc := range plan.Workloads {
		wcByName[wc.Workload.Name] = wc
	}

	anyDeclared := false
	gaps := newGapSet()
	type k8sDepViolation struct {
		stateID int
		wc      ir.WorkloadChange
		depName string
		depWC   ir.WorkloadChange
	}
	var violations []k8sDepViolation

	nodes := append([]ir.RolloutState(nil), g.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	for _, wc := range plan.Workloads {
		if len(wc.Workload.DependsOn) == 0 {
			continue
		}
		anyDeclared = true
		if !wc.Workload.Readiness.HasReadinessProbe {
			gaps.add(ir.EvidenceGap{
				Field:  fmt.Sprintf("Workload %s Readiness", wc.Workload.Name),
				Reason: "no readiness probe declared at all — absence is not evidence of dependency-awareness either way",
			})
			continue
		}
		if wc.Workload.Readiness.WaitsOnDependencies {
			continue
		}
		for _, depName := range wc.Workload.DependsOn {
			depWC, ok := wcByName[depName]
			if !ok {
				gaps.add(ir.EvidenceGap{
					Field:  fmt.Sprintf("Workload %s DependsOn %s", wc.Workload.Name, depName),
					Reason: "no workload with this name is part of this rollout plan",
				})
				continue
			}
			for _, s := range nodes {
				wReady := containsString(s.LiveVersionsFor(wc.Workload.ServiceName), wc.ToVersion)
				if !wReady {
					continue
				}
				dReady := containsString(s.LiveVersionsFor(depWC.Workload.ServiceName), depWC.ToVersion)
				if !dReady {
					violations = append(violations, k8sDepViolation{stateID: s.ID, wc: wc, depName: depName, depWC: depWC})
				}
			}
		}
	}

	if !anyDeclared {
		return ir.Diagnostic{
			InvariantID: RPK8S002,
			Verdict:     ir.VerdictSafe,
			Summary:     fmt.Sprintf("%s: not applicable (no workload declares a dependency)", RPK8S002),
		}
	}
	if len(violations) == 0 {
		if gaps.len() > 0 {
			return ir.Diagnostic{
				InvariantID:     RPK8S002,
				Verdict:         ir.VerdictUnknown,
				Summary:         fmt.Sprintf("%s: insufficient evidence to evaluate every declared dependency", RPK8S002),
				MissingEvidence: gaps.list(),
			}
		}
		return ir.Diagnostic{
			InvariantID: RPK8S002,
			Verdict:     ir.VerdictSafe,
			Summary:     fmt.Sprintf("%s: no reachable state admits traffic before a declared dependency is ready", RPK8S002),
		}
	}

	ids := make([]int, len(violations))
	for i, v := range violations {
		ids[i] = v.stateID
	}
	best := violations[shortestByStateID(g, ids)]
	path := g.ShortestPath(best.stateID)

	return ir.Diagnostic{
		InvariantID: RPK8S002,
		Verdict:     ir.VerdictUnsafe,
		Summary: fmt.Sprintf(
			"%s becomes ready and receives traffic while its declared dependency %s has not reached %s, and %s's readiness probe does not wait on it",
			best.wc.Workload.Name, best.depName, best.depWC.ToVersion, best.wc.Workload.Name),
		Evidence: []ir.Evidence{
			{Kind: ir.EvidenceRolloutStrategy, Description: fmt.Sprintf("%s declares DependsOn: %s, with WaitsOnDependencies: false", best.wc.Workload.Name, best.depName)},
		},
		Counterexample: &ir.Counterexample{
			Path:           path,
			ViolatingState: g.State(best.stateID),
			Outcome:        fmt.Sprintf("%s serves a request that needs %s, which is not yet ready, and the request fails or misbehaves", best.wc.Workload.Name, best.depName),
			RecommendedSequence: []string{
				fmt.Sprintf("add an init container or startup probe on %s that checks %s's readiness", best.wc.Workload.Name, best.depName),
				"declare rolloutproof.dev/waits-on-dependencies: \"true\" once that check is in place",
			},
		},
	}
}

// evaluateTerminationConflict implements RP-K8S-003 (docs/invariants.md).
//
// V1 scoping: architecture.md's WorkloadReplicaState.OldTerminating (a
// distinct per-replica-population dimension for "draining, potentially
// past a migration commit point") is not implemented in internal/graph's
// RolloutState (docs/graph's own package doc lists this as a known,
// deferred refinement, alongside dependency-based state pruning) — so
// this approximates the terminating population using the coexistence
// state RP-DB-001/002 already model (old and new both live: the window
// in which Kubernetes may be draining old replicas as new ones become
// ready), gated additionally on HasPreStopHook and a nonzero
// GracePeriodSeconds (evidence that a drain-time hook actually runs
// schema-dependent code).
//
// This approximation is only defensible for RollingUpdate: Recreate (and
// an unrecognized/unclassified strategy) has no coexistence state at
// all in this graph model — old fully stops before new starts, modeled
// as a single instantaneous edge (docs/graph's own package doc), not a
// state — so there is no reachable state in which "old" can even be
// named as the live version whose shutdown code might be running.
// Silently treating Recreate's *pre-rollout* old-only state (old is
// simply serving normally, nothing draining) as equivalent to
// "draining" would be a false positive this invariant must not produce;
// reporting it UNKNOWN for that workload, rather than guessing SAFE or
// UNSAFE, is what docs/vision.md §10 requires when the evidence a check
// needs — here, a state distinguishing "draining" from "not yet
// touched" — simply is not representable yet.
func evaluateTerminationConflict(plan ir.RolloutPlan, g *graph.Graph) ir.Diagnostic {
	drainingWorkloads := make(map[string]ir.WorkloadChange)
	for _, wc := range plan.Workloads {
		if wc.Workload.Termination.HasPreStopHook && wc.Workload.Termination.GracePeriodSeconds > 0 {
			drainingWorkloads[wc.Workload.ServiceName] = wc
		}
	}
	if len(drainingWorkloads) == 0 {
		return ir.Diagnostic{
			InvariantID: RPK8S003,
			Verdict:     ir.VerdictSafe,
			Summary:     fmt.Sprintf("%s: not applicable (no workload declares a PreStop hook with a nonzero grace period)", RPK8S003),
		}
	}

	gaps := newGapSet()
	evaluable := make(map[string]ir.WorkloadChange, len(drainingWorkloads))
	for name, wc := range drainingWorkloads {
		if wc.Workload.Strategy.Type != ir.StrategyRollingUpdate {
			gaps.add(ir.EvidenceGap{
				Field:  fmt.Sprintf("Workload %s termination window under strategy %s", wc.Workload.Name, wc.Workload.Strategy.Type),
				Reason: "this strategy has no reachable state distinctly representing old replicas draining (docs/graph models the old-stops-then-new-starts transition as a single edge, not a state) — cannot rule out a destructive migration committing during that window",
			})
			continue
		}
		evaluable[name] = wc
	}

	nodes := append([]ir.RolloutState(nil), g.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	type k8sTermViolation struct {
		stateID int
		wc      ir.WorkloadChange
		op      ir.CommittedOp
	}
	var violations []k8sTermViolation
	for _, s := range nodes {
		if s.SchemaState.Indeterminate {
			continue
		}
		if len(s.Live) < 2 {
			continue // draining is only representable as the coexistence state — see this function's doc comment
		}
		for _, cop := range s.SchemaState.CommittedOps {
			if cop.Op.Kind != ir.OpDropColumn {
				continue // a terminating replica's stale in-flight code assumes column existence; only removal is unambiguous here
			}
			for _, lv := range s.Live {
				wc, draining := evaluable[lv.ServiceName]
				if !draining || lv.Version != wc.FromVersion {
					continue // only the *old* (draining) version's shutdown code is at risk
				}
				violations = append(violations, k8sTermViolation{stateID: s.ID, wc: wc, op: cop})
			}
		}
	}

	if len(violations) == 0 {
		if gaps.len() > 0 {
			return ir.Diagnostic{
				InvariantID:     RPK8S003,
				Verdict:         ir.VerdictUnknown,
				Summary:         fmt.Sprintf("%s: insufficient evidence to evaluate every draining-eligible workload", RPK8S003),
				MissingEvidence: gaps.list(),
			}
		}
		return ir.Diagnostic{
			InvariantID: RPK8S003,
			Verdict:     ir.VerdictSafe,
			Summary:     fmt.Sprintf("%s: no reachable state commits a destructive migration while a draining replica with a PreStop hook is live", RPK8S003),
		}
	}

	ids := make([]int, len(violations))
	for i, v := range violations {
		ids[i] = v.stateID
	}
	best := violations[shortestByStateID(g, ids)]
	path := g.ShortestPath(best.stateID)
	target := best.op.Op.TargetColumn()

	return ir.Diagnostic{
		InvariantID: RPK8S003,
		Verdict:     ir.VerdictUnsafe,
		Summary: fmt.Sprintf(
			"migration %q drops %s while %s@%s (declares a PreStop hook, grace period %ds) may still be draining",
			best.op.MigrationID, target, best.wc.Workload.ServiceName, best.wc.FromVersion, best.wc.Workload.Termination.GracePeriodSeconds),
		Evidence: []ir.Evidence{
			{Kind: ir.EvidenceMigrationOp, Artifact: best.op.MigrationID, Description: fmt.Sprintf("migration drops %s", target)},
			{Kind: ir.EvidenceRolloutStrategy, Description: fmt.Sprintf("%s declares a PreStop hook with a %ds grace period", best.wc.Workload.Name, best.wc.Workload.Termination.GracePeriodSeconds)},
		},
		Counterexample: &ir.Counterexample{
			Path:           path,
			ViolatingState: g.State(best.stateID),
			Outcome:        fmt.Sprintf("the PreStop hook's shutdown code accesses %s during its grace period, after the column no longer exists", target),
			RecommendedSequence: []string{
				fmt.Sprintf("reduce %s's terminationGracePeriodSeconds or remove the PreStop hook's dependency on %s", best.wc.Workload.Name, target),
				"sequence the migration strictly after old replicas fully terminate",
			},
		},
	}
}

// evaluateUnsafeCompatibilityWindow implements RP-K8S-004
// (docs/invariants.md): a purely structural, plan-level check —
// MaxSurge/MaxUnavailable widening the coexistence window combined with
// any PhaseDuringRollout Destructive/ConditionallyDestructive migration
// not covered by an RP-DB-006 expand/contract declaration.
func evaluateUnsafeCompatibilityWindow(plan ir.RolloutPlan) ir.Diagnostic {
	covered := make(map[string]bool, len(plan.ExpandContractLinks))
	for _, link := range plan.ExpandContractLinks {
		covered[link.ContractMigrationID] = true
	}

	var widened []ir.WorkloadChange
	for _, wc := range plan.Workloads {
		if wc.Workload.Strategy.AllowsCoexistence(wc.Workload.Replicas) {
			widened = append(widened, wc)
		}
	}
	if len(widened) == 0 {
		return ir.Diagnostic{
			InvariantID: RPK8S004,
			Verdict:     ir.VerdictSafe,
			Summary:     fmt.Sprintf("%s: not applicable (no workload's strategy permits coexistence)", RPK8S004),
		}
	}

	for _, mt := range plan.Migrations {
		if mt.Phase != ir.PhaseDuringRollout {
			continue
		}
		if covered[mt.Migration.ID] {
			continue
		}
		for _, op := range mt.Migration.Operations {
			if op.Destructiveness != ir.Destructive && op.Destructiveness != ir.ConditionallyDestructive {
				continue
			}
			return ir.Diagnostic{
				InvariantID: RPK8S004,
				Verdict:     ir.VerdictUnsafe,
				Advisory:    true,
				Summary: fmt.Sprintf(
					"%s: migration %q commits a %s change during rollout while %s's strategy permits version coexistence, with no declared expand/contract sequencing (advisory — does not block the overall verdict; see docs/invariants.md RP-K8S-004 Limitations)",
					RPK8S004, mt.Migration.ID, op.Destructiveness, widened[0].Workload.Name),
				Evidence: []ir.Evidence{
					{Kind: ir.EvidenceMigrationOp, Artifact: mt.Migration.ID, Description: fmt.Sprintf("%s operation on %s.%s, phase during rollout", op.Kind, op.Table, op.Column)},
					{Kind: ir.EvidenceRolloutStrategy, Description: fmt.Sprintf("%s's strategy permits coexistence (MaxSurge/MaxUnavailable widen the window)", widened[0].Workload.Name)},
				},
				Counterexample: &ir.Counterexample{
					Outcome: "the structural pattern (destructive-during-rollout with coexistence) is present, independent of whether contract metadata confirms a specific reader/writer conflict",
					RecommendedSequence: []string{
						"declare an explicit expand/contract relationship (RP-DB-006) if this is an intentional expand/contract step",
						"or move this migration to phase \"after\" so it commits only once the rollout completes",
					},
				},
			}
		}
	}

	return ir.Diagnostic{
		InvariantID: RPK8S004,
		Verdict:     ir.VerdictSafe,
		Summary:     fmt.Sprintf("%s: no undeclared destructive migration is phased during a coexistence-permitting rollout", RPK8S004),
	}
}
