// Rollback safety (docs/architecture.md §5, docs/invariants.md RP-DB-007
// and RP-ROLLBACK-001/002/003) is evaluated separately from Evaluate's
// column-compatibility family: it needs plan.RollbackTarget and builds
// its own transition graph (graph.BuildRollback) rather than reasoning
// over the forward graph's reachable states, so it cannot share
// Evaluate's g-only signature.
//
// V1 scoping. A RolloutPlan describes exactly one rollout (one
// workload's transition plus its migrations); there is no multi-plan
// timeline for the CLI to place "the rollback point" within. RP-DB-007's
// "migrations committed before the rollback point" is therefore taken to
// mean every migration in the forward plan (all three phases chained, as
// the transition graph's own Target node already reflects) — i.e., V1
// evaluates "if this rollback were requested once the whole plan has
// committed, is it safe," which is both the conservative reading (latest
// possible schema state, most migrations already committed) and the only
// reading a single RolloutPlan can express without a declared
// intermediate rollback point.
package invariant

import (
	"fmt"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// RPDB007 is the invariant ID for "irreversible migration with required
// rollback" (docs/invariants.md) — the shared mechanism RP-ROLLBACK-001
// is a direct, user-facing application of.
const RPDB007 = "RP-DB-007"

// RPROLLBACK001 is the invariant ID for "target rollout can fail after an
// irreversible migration" (docs/invariants.md) — RP-DB-007 evaluated
// against this specific plan's declared RollbackTarget.
const RPROLLBACK001 = "RP-ROLLBACK-001"

// RPROLLBACK002 is the invariant ID for "old version cannot run against
// new schema" (docs/invariants.md): the RP-DB column-compatibility family
// re-evaluated against the rollback's own operational transition graph.
const RPROLLBACK002 = "RP-ROLLBACK-002"

// RPROLLBACK003 is the invariant ID for "rollback target violates
// compatibility invariant" (docs/invariants.md): the named aggregate of
// every rollback-relevant finding above, expressed as both a tri-state
// Diagnostic.Verdict and (in Diagnostic.RollbackVerdict) the finer
// four-state SAFE/CONDITIONALLY_SAFE/UNSAFE/UNKNOWN classification
// docs/architecture.md §5 defines.
const RPROLLBACK003 = "RP-ROLLBACK-003"

// notApplicableRollback returns the "no rollback declared" Diagnostic
// every rollback invariant reports identically when plan.RollbackTarget
// is nil — not evaluated, not UNKNOWN, since there is nothing to
// evaluate (docs/architecture.md §2.6).
func notApplicableRollback(id string) ir.Diagnostic {
	return ir.Diagnostic{
		InvariantID: id,
		Verdict:     ir.VerdictSafe,
		Summary:     fmt.Sprintf("%s: not applicable (no rollback target declared)", id),
	}
}

// EvaluateRollbackPlan runs the full rollback safety family (RP-DB-007,
// RP-ROLLBACK-001/002/003) against forward's declared RollbackTarget.
// Returns exactly those four diagnostics, each reporting "not applicable"
// when forward.RollbackTarget is nil, rather than an empty slice — this
// keeps EvaluateRollbackPlan's contract identical to Evaluate's ("one
// Diagnostic per implemented invariant, always"), which is what lets a
// report count "N invariants evaluated" honestly regardless of whether a
// rollback was declared.
func EvaluateRollbackPlan(forward ir.RolloutPlan, forwardGraph *graph.Graph, services map[ir.ServiceKey]ir.Service) []ir.Diagnostic {
	if forward.RollbackTarget == nil {
		return []ir.Diagnostic{
			notApplicableRollback(RPDB007),
			notApplicableRollback(RPROLLBACK001),
			notApplicableRollback(RPROLLBACK002),
			notApplicableRollback(RPROLLBACK003),
		}
	}

	irreversible := evaluateIrreversibleBeforeRollback(forward, forwardGraph, services)
	rollback001 := irreversible
	rollback001.InvariantID = RPROLLBACK001

	rollback002 := evaluateOldVersionAgainstCurrentSchema(forward, forwardGraph, services)

	aggregate := evaluateRollbackAggregate(forward, forwardGraph, services, rollback001, rollback002)

	return []ir.Diagnostic{irreversible, rollback001, rollback002, aggregate}
}

// evaluateIrreversibleBeforeRollback implements RP-DB-007
// (docs/invariants.md): no migration committed before the rollback point
// (V1 scoping: the forward graph's Target node — see this file's package
// doc) may be Irreversible, or ConditionallyReversible with data written
// under the new structure, while the rollback target's declared schema
// access depends on what that migration changed.
func evaluateIrreversibleBeforeRollback(forward ir.RolloutPlan, forwardGraph *graph.Graph, services map[ir.ServiceKey]ir.Service) ir.Diagnostic {
	rollbackKey := ir.ServiceKey{Name: forward.RollbackTarget.Workload.ServiceName, Version: forward.RollbackTarget.ToVersion}
	rollbackSvc, ok := services[rollbackKey]
	if !ok {
		return ir.Diagnostic{
			InvariantID: RPDB007,
			Verdict:     ir.VerdictUnknown,
			Summary:     fmt.Sprintf("%s: insufficient evidence to evaluate the rollback target's schema dependencies", RPDB007),
			MissingEvidence: []ir.EvidenceGap{{
				Field:  fmt.Sprintf("Service %s@%s", rollbackKey.Name, rollbackKey.Version),
				Reason: "no contract metadata file found for the rollback target version",
			}},
		}
	}

	targetState := forwardGraph.State(forwardGraph.Target)
	gaps := newGapSet()
	var evidence []ir.Evidence
	verdict := ir.VerdictSafe

	for _, cop := range targetState.SchemaState.CommittedOps {
		if cop.Op.Kind != ir.OpDropColumn && cop.Op.Kind != ir.OpRenameColumn {
			continue // RP-DB-007 concerns column existence, per its own algorithm's Reversibility check on drop/rename-classified ops
		}
		target := cop.Op.TargetColumn()
		if !rollbackSvc.TouchesTable(target.Table) {
			gaps.add(ir.EvidenceGap{
				Field:  fmt.Sprintf("Service %s@%s schema access on table %s", rollbackKey.Name, rollbackKey.Version, target.Table),
				Reason: "contract metadata for the rollback target version does not mention this table",
			})
			continue
		}
		if !rollbackSvc.ReadsColumn(target) && !rollbackSvc.WritesColumn(target) {
			continue // declared facts say the rollback target does not depend on this column
		}

		switch cop.Op.Reversibility {
		case ir.Irreversible:
			verdict = ir.VerdictUnsafe
			evidence = append(evidence, ir.Evidence{
				Kind:        ir.EvidenceMigrationOp,
				Artifact:    cop.MigrationID,
				Description: fmt.Sprintf("commits an irreversible change to %s, which the rollback target still reads or writes", target),
			})
		case ir.ConditionallyReversible:
			if anyLiveVersionWritesColumn(forwardGraph, services, target) {
				verdict = ir.VerdictUnsafe
				evidence = append(evidence, ir.Evidence{
					Kind:        ir.EvidenceMigrationOp,
					Artifact:    cop.MigrationID,
					Description: fmt.Sprintf("commits a conditionally reversible change to %s, and a live version during the forward rollout declares it writes that column — the reversal would lose that data (docs/architecture.md §5)", target),
				})
			}
		case ir.Reversible:
			// A structurally reversible op (docs/architecture.md §2.4)
			// poses no RP-DB-007 hazard by itself; RP-ROLLBACK-002 still
			// separately checks whether the rollback target runs
			// correctly against the *un-reverted* current schema.
		default:
			gaps.add(ir.EvidenceGap{
				Field:  fmt.Sprintf("%s Reversibility (migration %q)", target, cop.MigrationID),
				Reason: "this operation's reversibility could not be classified",
			})
		}
	}

	if verdict == ir.VerdictUnsafe {
		return ir.Diagnostic{
			InvariantID: RPDB007,
			Verdict:     ir.VerdictUnsafe,
			Summary:     fmt.Sprintf("%s: an irreversible (or data-losing) migration committed before the rollback point, which the rollback target depends on", RPDB007),
			Evidence:    evidence,
			Counterexample: &ir.Counterexample{
				ViolatingState: targetState,
				Outcome:        "the rollback target depends on data or structure this migration has already irreversibly removed",
				RecommendedSequence: []string{
					"if the underlying data still exists elsewhere, restore it before completing the rollback",
					"otherwise, treat this as a forward-only change: the rollback target cannot be safely restored against the current schema",
				},
			},
		}
	}
	if gaps.len() > 0 {
		return ir.Diagnostic{
			InvariantID:     RPDB007,
			Verdict:         ir.VerdictUnknown,
			Summary:         fmt.Sprintf("%s: insufficient evidence to evaluate every migration committed before the rollback point", RPDB007),
			MissingEvidence: gaps.list(),
		}
	}
	return ir.Diagnostic{
		InvariantID: RPDB007,
		Verdict:     ir.VerdictSafe,
		Summary:     fmt.Sprintf("%s: no migration committed before the rollback point is irreversible in a way the rollback target depends on", RPDB007),
	}
}

// anyLiveVersionWritesColumn reports whether any service version live in
// any reachable state of g declares (per services) that it writes col —
// the evidence docs/architecture.md §5 uses to decide whether a
// ConditionallyReversible change actually had data written under the new
// structure.
func anyLiveVersionWritesColumn(g *graph.Graph, services map[ir.ServiceKey]ir.Service, col ir.ColumnRef) bool {
	for _, s := range g.Nodes {
		for _, lv := range s.Live {
			svc, ok := services[ir.ServiceKey{Name: lv.ServiceName, Version: lv.Version}]
			if ok && svc.WritesColumn(col) {
				return true
			}
		}
	}
	return false
}

// evaluateOldVersionAgainstCurrentSchema implements RP-ROLLBACK-002
// (docs/invariants.md): treats the rollback as its own RolloutPlan
// (graph.BuildRollback) whose target schema is whatever the forward plan
// actually committed (no migration reversal), and re-runs the full
// column-compatibility family against it — "the same invariant family
// evaluated on a different RolloutPlan," per the docs, aggregated here
// into one Diagnostic so the rollback family stays exactly four entries
// regardless of how many underlying RP-DB checks fire.
func evaluateOldVersionAgainstCurrentSchema(forward ir.RolloutPlan, forwardGraph *graph.Graph, services map[ir.ServiceKey]ir.Service) ir.Diagnostic {
	rbPlan, rbGraph, err := graph.BuildRollback(forward, forwardGraph)
	if err != nil {
		return ir.Diagnostic{
			InvariantID: RPROLLBACK002,
			Verdict:     ir.VerdictUnknown,
			Summary:     fmt.Sprintf("%s: could not construct the rollback's transition graph", RPROLLBACK002),
			MissingEvidence: []ir.EvidenceGap{{
				Field:  "RolloutPlan.RollbackTarget",
				Reason: err.Error(),
			}},
		}
	}

	// vacuousWhenNoOp=false: the rollback graph legitimately commits no
	// migrations of its own (operational rollback only), but its
	// BaseSchema already reflects whatever the forward plan committed —
	// see evaluateColumnFamily's doc comment (rpdb.go).
	subDiags := evaluateColumnFamily(rbPlan, rbGraph, services, false)

	verdict := ir.VerdictSafe
	var evidence []ir.Evidence
	var gaps []ir.EvidenceGap
	var counterexample *ir.Counterexample
	for _, d := range subDiags {
		if d.Verdict == ir.VerdictSafe {
			continue
		}
		verdict = ir.Combine(verdict, d.Verdict)
		evidence = append(evidence, ir.Evidence{
			Kind:        ir.EvidenceServiceContract,
			Description: fmt.Sprintf("%s %s against the rollback target: %s", d.InvariantID, d.Verdict, d.Summary),
		})
		gaps = append(gaps, d.MissingEvidence...)
		if d.Verdict == ir.VerdictUnsafe && counterexample == nil && d.Counterexample != nil {
			counterexample = d.Counterexample
		}
	}

	switch verdict {
	case ir.VerdictUnsafe:
		return ir.Diagnostic{
			InvariantID:    RPROLLBACK002,
			Verdict:        ir.VerdictUnsafe,
			Summary:        fmt.Sprintf("%s: the rollback target cannot run correctly against the current, un-reverted schema", RPROLLBACK002),
			Evidence:       evidence,
			Counterexample: counterexample,
		}
	case ir.VerdictUnknown:
		return ir.Diagnostic{
			InvariantID:     RPROLLBACK002,
			Verdict:         ir.VerdictUnknown,
			Summary:         fmt.Sprintf("%s: insufficient evidence to confirm the rollback target runs correctly against the current schema", RPROLLBACK002),
			Evidence:        evidence,
			MissingEvidence: gaps,
		}
	default:
		return ir.Diagnostic{
			InvariantID: RPROLLBACK002,
			Verdict:     ir.VerdictSafe,
			Summary:     fmt.Sprintf("%s: the rollback target runs correctly against the current, un-reverted schema", RPROLLBACK002),
		}
	}
}

// verdictToRollback maps a tri-state Verdict onto ir.RollbackVerdict's
// SAFE/UNSAFE/UNKNOWN positions, leaving CONDITIONALLY_SAFE to
// evaluateRollbackAggregate's own combination with the per-op
// EvaluateRollback classification (rollback.go), which is the only
// source of that distinction.
func verdictToRollback(v ir.Verdict) ir.RollbackVerdict {
	switch v {
	case ir.VerdictSafe:
		return ir.RollbackSafe
	case ir.VerdictUnsafe:
		return ir.RollbackUnsafe
	default:
		return ir.RollbackUnknown
	}
}

// evaluateRollbackAggregate implements RP-ROLLBACK-003 (docs/invariants.md):
// not a new checking algorithm, but the named combination of every
// rollback-relevant finding into one overall rollback verdict — both the
// ordinary tri-state Diagnostic.Verdict, and, in Diagnostic.RollbackVerdict,
// the finer four-state classification docs/architecture.md §5 defines
// (SAFE/CONDITIONALLY_SAFE/UNSAFE/UNKNOWN), reusing the per-op
// classification already implemented in rollback.go's EvaluateRollback —
// which is the only source of CONDITIONALLY_SAFE, since a
// merely-Reversible-in-principle change still requires an explicit
// migration-reversal action neither this plan nor RP-ROLLBACK-002's
// operational-only rollback graph takes.
func evaluateRollbackAggregate(forward ir.RolloutPlan, forwardGraph *graph.Graph, services map[ir.ServiceKey]ir.Service, rollback001, rollback002 ir.Diagnostic) ir.Diagnostic {
	rollbackKey := ir.ServiceKey{Name: forward.RollbackTarget.Workload.ServiceName, Version: forward.RollbackTarget.ToVersion}
	rollbackSvc, ok := services[rollbackKey]

	var rbVerdict ir.RollbackVerdict
	if !ok {
		rbVerdict = ir.RollbackUnknown
	} else {
		targetState := forwardGraph.State(forwardGraph.Target)
		rbVerdict = ir.CombineRollback(
			EvaluateRollback(targetState.SchemaState.CommittedOps, rollbackSvc),
			verdictToRollback(rollback002.Verdict),
		)
	}

	overall := ir.Combine(rollback001.Verdict, rollback002.Verdict)
	// A CONDITIONALLY_SAFE rollback is not a confirmed-safe answer — the
	// plan as declared does not itself revert the schema change, so the
	// tri-state Verdict must not silently read as SAFE (docs/vision.md
	// §10's "never silently safe" applies to this aggregate exactly as it
	// does to every other invariant).
	if rbVerdict == ir.RollbackConditionallySafe && overall == ir.VerdictSafe {
		overall = ir.VerdictUnknown
	}

	var counterexample *ir.Counterexample
	if rollback001.Verdict == ir.VerdictUnsafe {
		counterexample = rollback001.Counterexample
	} else if rollback002.Verdict == ir.VerdictUnsafe {
		counterexample = rollback002.Counterexample
	}

	return ir.Diagnostic{
		InvariantID: RPROLLBACK003,
		Verdict:     overall,
		Summary: fmt.Sprintf(
			"%s: overall rollback classification is %s (RP-ROLLBACK-001: %s, RP-ROLLBACK-002: %s)",
			RPROLLBACK003, rbVerdict, rollback001.Verdict, rollback002.Verdict),
		Counterexample:  counterexample,
		RollbackVerdict: rbVerdict,
	}
}
