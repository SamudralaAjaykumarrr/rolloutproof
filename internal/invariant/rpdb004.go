package invariant

import (
	"fmt"
	"sort"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// RPDB004 is the invariant ID for "unsafe NOT NULL introduction"
// (docs/invariants.md).
const RPDB004 = "RP-DB-004"

// notNullOp reports whether op tightens a column to NOT NULL in a way
// that can reject a row an old writer still sends: either OpSetNotNull
// directly, or OpAddColumn declared NOT NULL with no DEFAULT (a DEFAULT
// makes Postgres supply the value for an old writer that omits the
// column, which is why classifyAddColumn — internal/parser/sql/classify.go
// — only marks that combination ConditionallyDestructive).
func notNullOp(op ir.MigrationOp) bool {
	if op.Kind == ir.OpSetNotNull {
		return true
	}
	return op.Kind == ir.OpAddColumn && !op.Nullable && op.Default == nil
}

// findNotNullOp finds the CommittedOp, if any, responsible for col being
// NOT NULL with no default in this state — scanning in reverse so the
// most recently committed matching op wins. found=false means the
// column was already NOT NULL with no default in the plan's BaseSchema
// itself (most relevantly: when re-evaluating a rollback's own graph,
// whose BaseSchema is whatever the forward plan already committed —
// rollback_plan.go's evaluateOldVersionAgainstCurrentSchema).
func findNotNullOp(committed []ir.CommittedOp, col ir.ColumnRef) (op ir.CommittedOp, found bool) {
	for i := len(committed) - 1; i >= 0; i-- {
		cop := committed[i]
		if cop.Op.Table == col.Table && cop.Op.Column == col.Column && notNullOp(cop.Op) {
			return cop, true
		}
	}
	return ir.CommittedOp{}, false
}

// evaluateNotNullIntroduction implements RP-DB-004 (docs/invariants.md): a
// live version must not write rows to a table without declaring it
// populates a column that is NOT NULL with no default in the schema
// actually committed at every reachable state where that version is
// live — checked directly against the schema snapshot (docs/ir.Column's
// Nullable/Default fields), not by scanning for a locally committed
// OpSetNotNull/OpAddColumn: the same generalization
// evaluateColumnExistence (rpdb.go) applies to RP-DB-001/002, and for the
// same reason — a constraint already present in a graph's BaseSchema
// (e.g. a rollback's, whose BaseSchema is whatever the forward plan
// already committed) is exactly as real a hazard as one this graph's own
// migrations introduce.
//
// vacuousWhenNoOp mirrors evaluateColumnExistence's parameter: true for
// the ordinary forward-plan entry point (Evaluate), where
// docs/scenario-corpus.md SC-SAFE-004 makes "no migration in this plan"
// vacuously SAFE; false when re-evaluating a rollback's own graph
// (rollback_plan.go), which legitimately commits no migrations of its
// own by design (RP-ROLLBACK-002's operational-only scope) while still
// needing this check to run against its BaseSchema.
func evaluateNotNullIntroduction(g *graph.Graph, services map[ir.ServiceKey]ir.Service, vacuousWhenNoOp bool) ir.Diagnostic {
	if vacuousWhenNoOp && !graphHasAnyCommittedOp(g) {
		return ir.Diagnostic{
			InvariantID: RPDB004,
			Verdict:     ir.VerdictSafe,
			Summary:     fmt.Sprintf("%s: not applicable (this plan commits no migration)", RPDB004),
		}
	}

	var violations []violation
	gaps := newGapSet()

	nodes := append([]ir.RolloutState(nil), g.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	for _, s := range nodes {
		if s.SchemaState.Indeterminate {
			gaps.add(ir.EvidenceGap{
				Field:  fmt.Sprintf("RolloutState %d SchemaState", s.ID),
				Reason: "a migration operation in this reachable state could not be classified (unsupported SQL), so its effect on the schema is unknown",
			})
			continue
		}
		for _, table := range s.SchemaState.Schema.Tables() {
			for _, col := range table.Columns() {
				if col.Nullable || col.Default != nil {
					continue
				}
				target := ir.ColumnRef{Table: table.Name(), Column: col.Name}
				for _, lv := range s.Live {
					key := ir.ServiceKey{Name: lv.ServiceName, Version: lv.Version}
					svc, ok := services[key]
					if !ok {
						gaps.add(ir.EvidenceGap{
							Field:  fmt.Sprintf("Service %s@%s", lv.ServiceName, lv.Version),
							Reason: "no contract metadata file found for this service version",
						})
						continue
					}
					if !svc.TouchesTable(target.Table) {
						gaps.add(ir.EvidenceGap{
							Field:  fmt.Sprintf("Service %s@%s schema access on table %s", lv.ServiceName, lv.Version, target.Table),
							Reason: "contract metadata for this service version does not mention this table",
						})
						continue
					}
					if !svc.WritesTable(target.Table) {
						// Declared facts about this table exist, and they say
						// this version never writes to it at all — a positive,
						// closed-world fact that this version cannot violate
						// RP-DB-004 (docs/adr/0004), not an evidence gap.
						continue
					}
					if svc.WritesColumn(target) {
						continue
					}
					removingOp, found := findNotNullOp(s.SchemaState.CommittedOps, target)
					var opPtr *ir.CommittedOp
					if found {
						opPtr = &removingOp
					}
					violations = append(violations, violation{stateID: s.ID, col: target, removingOp: opPtr, svcVersion: lv, svc: svc, liveInState: s.Live})
				}
			}
		}
	}

	if len(violations) == 0 {
		if gaps.len() > 0 {
			return ir.Diagnostic{
				InvariantID:     RPDB004,
				Verdict:         ir.VerdictUnknown,
				Summary:         fmt.Sprintf("%s: insufficient evidence to evaluate every reachable state", RPDB004),
				MissingEvidence: gaps.list(),
			}
		}
		return ir.Diagnostic{
			InvariantID: RPDB004,
			Verdict:     ir.VerdictSafe,
			Summary:     fmt.Sprintf("%s: no reachable state violates this invariant", RPDB004),
		}
	}

	best := selectShortest(g, violations)
	path := g.ShortestPath(best.stateID)
	target := best.col

	var causeText, migID string
	if best.removingOp != nil {
		causeText = fmt.Sprintf("migration %q requires %s to be NOT NULL with no compensating default", best.removingOp.MigrationID, target)
		migID = best.removingOp.MigrationID
	} else {
		causeText = fmt.Sprintf("%s is NOT NULL with no default in the schema committed at this point in the rollout", target)
	}

	summary := fmt.Sprintf(
		"%s@%s writes to %s without declaring it populates %s (%s)",
		best.svcVersion.ServiceName, best.svcVersion.Version, target.Table, target.Column, causeText)

	evidence := []ir.Evidence{
		{
			Kind:        ir.EvidenceMigrationOp,
			Artifact:    migID,
			Description: causeText,
		},
		{
			Kind:        ir.EvidenceSchemaWrite,
			Artifact:    best.svc.SourceFile,
			Description: fmt.Sprintf("%s@%s declares writes to %s but does not declare it populates %s", best.svcVersion.ServiceName, best.svcVersion.Version, target.Table, target.Column),
		},
		{
			Kind:        ir.EvidenceRolloutStrategy,
			Description: describeReachability(best.liveInState),
		},
	}

	recommended := []string{
		fmt.Sprintf("deploy a release of %s that populates %s on every write to %s", best.svcVersion.ServiceName, target.Column, target.Table),
		"wait for that release to complete its rollout",
		fmt.Sprintf("verify no live version writes %s without populating %s", target.Table, target.Column),
	}
	if migID != "" {
		recommended = append(recommended, fmt.Sprintf("apply migration %q", migID))
	}

	return ir.Diagnostic{
		InvariantID: RPDB004,
		Verdict:     ir.VerdictUnsafe,
		Summary:     summary,
		Evidence:    evidence,
		Counterexample: &ir.Counterexample{
			Path:                path,
			ViolatingState:      g.State(best.stateID),
			Outcome:             fmt.Sprintf("the write to %s is rejected outright by the NOT NULL constraint", target),
			RecommendedSequence: recommended,
		},
		// RollbackVerdict is deliberately left at its zero value
		// (RollbackUnknown): RP-DB-004 is a forward-write hazard, not a
		// data-loss-on-revert hazard, so EvaluateRollback's drop/rename
		// mechanism does not apply here (docs/architecture.md §5) and
		// this diagnostic makes no rollback claim rather than a
		// misleading one (ir.Diagnostic's zero-value-safety convention).
	}
}
