package invariant

import (
	"fmt"
	"sort"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// RPORDER001 is the invariant ID for "provider upgraded before consumer
// compatibility" (docs/invariants.md).
const RPORDER001 = "RP-ORDER-001"

// RPORDER002 is the invariant ID for "consumer upgraded before provider
// support" (docs/invariants.md) — docs describe this as "symmetric to
// RP-ORDER-001," and evaluateOrdering's single reachable-state scan
// already checks every live consumer against every live provider
// regardless of which WorkloadChange caused a given state to be
// reachable, so both IDs report the same underlying finding — the "thin
// wrapper, shared code path, distinct ID" pattern RP-DB-005 already uses
// for RP-DB-001/002 (docs/invariants.md RP-DB-005's own justification).
const RPORDER002 = "RP-ORDER-002"

type orderViolation struct {
	stateID     int
	consumerLV  ir.LiveVersion
	consumer    ir.Service
	providerLV  ir.LiveVersion
	dep         ir.ServiceDependency
	liveInState []ir.LiveVersion
}

// EvaluateOrder implements RP-ORDER-001/002 (docs/invariants.md): a live
// consumer's declared ServiceDependency.MinCompatibleVersion for a
// provider must be satisfied by whatever version of that provider is
// live in the same reachable state — evaluated symmetrically regardless
// of which side's WorkloadChange produced the state, since docs describe
// RP-ORDER-002 as exactly RP-ORDER-001 evaluated from the other
// direction.
func EvaluateOrder(g *graph.Graph, services map[ir.ServiceKey]ir.Service) []ir.Diagnostic {
	if !anyDependencyDeclared(services) {
		return []ir.Diagnostic{notApplicableOrder(RPORDER001), notApplicableOrder(RPORDER002)}
	}

	var violations []orderViolation
	gaps := newGapSet()

	nodes := append([]ir.RolloutState(nil), g.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	for _, s := range nodes {
		for _, clv := range s.Live {
			consumer, ok := services[ir.ServiceKey{Name: clv.ServiceName, Version: clv.Version}]
			if !ok {
				gaps.add(ir.EvidenceGap{
					Field:  fmt.Sprintf("Service %s@%s", clv.ServiceName, clv.Version),
					Reason: "no contract metadata file found for this service version",
				})
				continue
			}
			for _, dep := range consumer.DependsOn() {
				if dep.MinCompatibleVersion == "" {
					continue // declared, but no version constraint — nothing to check (docs/adr/0007)
				}
				matched := false
				for _, plv := range s.Live {
					if plv.ServiceName != dep.ServiceName {
						continue
					}
					matched = true
					cmp, ok := compareVersions(plv.Version, dep.MinCompatibleVersion)
					if !ok {
						gaps.add(ir.EvidenceGap{
							Field:  fmt.Sprintf("version comparison %s vs %s", plv.Version, dep.MinCompatibleVersion),
							Reason: "versions are not comparable under RolloutProof's dotted-numeric fallback scheme (docs/architecture.md §2.1.1's opaque-unordered case)",
						})
						continue
					}
					if cmp < 0 {
						violations = append(violations, orderViolation{
							stateID: s.ID, consumerLV: clv, consumer: consumer, providerLV: plv, dep: dep, liveInState: s.Live,
						})
					}
				}
				if !matched {
					gaps.add(ir.EvidenceGap{
						Field:  fmt.Sprintf("live version of %s (required by %s@%s)", dep.ServiceName, clv.ServiceName, clv.Version),
						Reason: "no live service version in this reachable state matches the declared dependency's service name",
					})
				}
			}
		}
	}

	rporder001 := buildOrderDiagnostic(RPORDER001, g, violations, gaps)
	rporder002 := buildOrderDiagnostic(RPORDER002, g, violations, gaps)
	return []ir.Diagnostic{rporder001, rporder002}
}

func anyDependencyDeclared(services map[ir.ServiceKey]ir.Service) bool {
	for _, svc := range services {
		if len(svc.DependsOn()) > 0 {
			return true
		}
	}
	return false
}

func notApplicableOrder(id string) ir.Diagnostic {
	return ir.Diagnostic{
		InvariantID: id,
		Verdict:     ir.VerdictSafe,
		Summary:     fmt.Sprintf("%s: not applicable (no service declares a version-constrained dependency)", id),
	}
}

func buildOrderDiagnostic(invariantID string, g *graph.Graph, violations []orderViolation, gaps *gapSet) ir.Diagnostic {
	if len(violations) == 0 {
		if gaps.len() > 0 {
			return ir.Diagnostic{
				InvariantID:     invariantID,
				Verdict:         ir.VerdictUnknown,
				Summary:         fmt.Sprintf("%s: insufficient evidence to evaluate every reachable state", invariantID),
				MissingEvidence: gaps.list(),
			}
		}
		return ir.Diagnostic{
			InvariantID: invariantID,
			Verdict:     ir.VerdictSafe,
			Summary:     fmt.Sprintf("%s: no reachable state violates this invariant", invariantID),
		}
	}

	ids := make([]int, len(violations))
	for i, v := range violations {
		ids[i] = v.stateID
	}
	best := violations[shortestByStateID(g, ids)]
	path := g.ShortestPath(best.stateID)

	return ir.Diagnostic{
		InvariantID: invariantID,
		Verdict:     ir.VerdictUnsafe,
		Summary: fmt.Sprintf(
			"%s@%s requires %s at least %s, but %s@%s is live",
			best.consumerLV.ServiceName, best.consumerLV.Version, best.dep.ServiceName, best.dep.MinCompatibleVersion,
			best.providerLV.ServiceName, best.providerLV.Version),
		Evidence: []ir.Evidence{
			{Kind: ir.EvidenceServiceContract, Artifact: best.consumer.SourceFile, Description: fmt.Sprintf("%s@%s declares a dependency on %s >= %s", best.consumerLV.ServiceName, best.consumerLV.Version, best.dep.ServiceName, best.dep.MinCompatibleVersion)},
			{Kind: ir.EvidenceRolloutStrategy, Description: describeReachability(best.liveInState)},
		},
		Counterexample: &ir.Counterexample{
			Path:           path,
			ViolatingState: g.State(best.stateID),
			Outcome:        fmt.Sprintf("%s@%s calls %s assuming capability introduced in %s or later, which %s@%s does not have", best.consumerLV.ServiceName, best.consumerLV.Version, best.dep.ServiceName, best.dep.MinCompatibleVersion, best.providerLV.ServiceName, best.providerLV.Version),
			RecommendedSequence: []string{
				fmt.Sprintf("deploy %s to at least %s before %s@%s becomes live", best.dep.ServiceName, best.dep.MinCompatibleVersion, best.consumerLV.ServiceName, best.consumerLV.Version),
				"wait for that rollout to complete",
				fmt.Sprintf("verify no live version of %s is below %s", best.dep.ServiceName, best.dep.MinCompatibleVersion),
			},
		},
	}
}
