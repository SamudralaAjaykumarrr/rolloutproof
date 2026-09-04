package invariant

import (
	"fmt"
	"sort"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// RPDB006 is the invariant ID for "expand/contract sequence violation"
// (docs/invariants.md).
const RPDB006 = "RP-DB-006"

// evaluateExpandContractSequence implements RP-DB-006 (docs/invariants.md):
// an explicitly declared expand/contract migration pair's contract step
// must be sequenced strictly after full rollout completion of the plan
// that introduced the expand step — never in the same rollout, and never
// with an ordering ambiguous enough that a consumer of the old shape
// might still be live when it commits.
//
// V1 scoping: a RolloutPlan describes exactly one workload's transition
// (internal/parser/rolloutplan's directory convention), so "full rollout
// completion of every service version that had a declared dependency on
// the expand step's target" (docs/invariants.md's general algorithm)
// reduces, for a single-plan CLI invocation, to a structural check on the
// contract step's own MigrationPhase: docs/architecture.md §3.2 step 3
// already guarantees that a PhaseAfterRollout migration is only ever
// evaluated against the fully-rolled-out end state, so a contract step
// declared with any other phase is unsafe by construction, independent of
// whether RP-DB-001/002/004/005 additionally have enough evidence to
// catch the same hazard directly (docs/invariants.md RP-DB-006's Unsafe
// example makes the same point about RP-DB-001/002).
func evaluateExpandContractSequence(plan ir.RolloutPlan, g *graph.Graph) ir.Diagnostic {
	if len(plan.ExpandContractLinks) == 0 {
		return ir.Diagnostic{
			InvariantID: RPDB006,
			Verdict:     ir.VerdictSafe,
			Summary:     fmt.Sprintf("%s: not applicable (no expand/contract relationship declared)", RPDB006),
		}
	}

	phaseByID := make(map[string]ir.MigrationPhase, len(plan.Migrations))
	for _, mt := range plan.Migrations {
		phaseByID[mt.Migration.ID] = mt.Phase
	}

	var violatingIDs []string
	for _, link := range plan.ExpandContractLinks {
		if phase, ok := phaseByID[link.ContractMigrationID]; !ok || phase != ir.PhaseAfterRollout {
			violatingIDs = append(violatingIDs, link.ContractMigrationID)
		}
	}

	if len(violatingIDs) == 0 {
		return ir.Diagnostic{
			InvariantID: RPDB006,
			Verdict:     ir.VerdictSafe,
			Summary:     fmt.Sprintf("%s: every declared expand/contract pair's contract step is sequenced after full rollout completion", RPDB006),
		}
	}

	sort.Strings(violatingIDs)
	contractID := violatingIDs[0]

	// Locate the shortest reachable state where the contract step
	// commits, for a concrete, evidence-backed counterexample — falling
	// back to the graph's overall target if, unusually, no node's
	// CommittedOps trail includes it (should not occur for a
	// well-formed plan, but a fallback keeps this defensive rather than
	// panicking on an index it did not find).
	var ids []int
	for _, s := range g.Nodes {
		for _, cop := range s.SchemaState.CommittedOps {
			if cop.MigrationID == contractID {
				ids = append(ids, s.ID)
				break
			}
		}
	}
	stateID := g.Target
	if len(ids) > 0 {
		stateID = ids[shortestByStateID(g, ids)]
	}
	path := g.ShortestPath(stateID)

	return ir.Diagnostic{
		InvariantID: RPDB006,
		Verdict:     ir.VerdictUnsafe,
		Summary: fmt.Sprintf(
			"contract migration %q is not sequenced strictly after full rollout completion, so consumers of the pre-expand shape may still be live when it commits",
			contractID),
		Evidence: []ir.Evidence{
			{
				Kind:        ir.EvidenceMigrationOp,
				Artifact:    contractID,
				Description: "declared as the contract step of an explicit expand/contract relationship, but its migration phase does not place it after full rollout completion",
			},
		},
		Counterexample: &ir.Counterexample{
			Path:           path,
			ViolatingState: g.State(stateID),
			RecommendedSequence: []string{
				"confirm the release that stops depending on the expanded shape has completed its rollout",
				fmt.Sprintf("schedule the contract migration %q with phase \"after\", in a separate, later rollout plan if needed", contractID),
			},
		},
	}
}
