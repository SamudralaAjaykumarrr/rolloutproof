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

// evaluateNotNullIntroduction implements RP-DB-004 (docs/invariants.md): a
// migration tightening a column to NOT NULL must not commit while a live
// version writes rows to that table without declaring it populates the
// column — Postgres rejects such a write outright once the constraint is
// in effect.
func evaluateNotNullIntroduction(g *graph.Graph, services map[ir.ServiceKey]ir.Service) ir.Diagnostic {
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
		for _, cop := range s.SchemaState.CommittedOps {
			if !notNullOp(cop.Op) {
				continue
			}
			target := cop.Op.TargetColumn()
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
				if !svc.WritesColumn(target) {
					violations = append(violations, violation{stateID: s.ID, op: cop, svcVersion: lv, svc: svc, liveInState: s.Live})
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
	target := best.op.Op.TargetColumn()

	summary := fmt.Sprintf(
		"migration %q requires %s to be NOT NULL (via %s) while %s@%s writes to %s without declaring it populates %s",
		best.op.MigrationID, target, best.op.Op.Kind, best.svcVersion.ServiceName, best.svcVersion.Version, target.Table, target.Column)

	evidence := []ir.Evidence{
		{
			Kind:        ir.EvidenceMigrationOp,
			Artifact:    best.op.MigrationID,
			Locator:     best.op.Op.Kind.String(),
			Line:        best.op.Op.SourceLine,
			Description: fmt.Sprintf("migration requires %s to be NOT NULL with no compensating default", target),
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

	return ir.Diagnostic{
		InvariantID: RPDB004,
		Verdict:     ir.VerdictUnsafe,
		Summary:     summary,
		Evidence:    evidence,
		Counterexample: &ir.Counterexample{
			Path:           path,
			ViolatingState: g.State(best.stateID),
			RecommendedSequence: []string{
				fmt.Sprintf("deploy a release of %s that populates %s on every write to %s", best.svcVersion.ServiceName, target.Column, target.Table),
				"wait for that release to complete its rollout",
				fmt.Sprintf("verify no live version writes %s without populating %s", target.Table, target.Column),
				fmt.Sprintf("apply migration %q", best.op.MigrationID),
			},
		},
		// RollbackVerdict is deliberately left at its zero value
		// (RollbackUnknown): RP-DB-004 is a forward-write hazard, not a
		// data-loss-on-revert hazard, so EvaluateRollback's drop/rename
		// mechanism does not apply here (docs/architecture.md §5) and
		// this diagnostic makes no rollback claim rather than a
		// misleading one (ir.Diagnostic's zero-value-safety convention).
	}
}
