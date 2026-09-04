package invariant

import (
	"fmt"
	"sort"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// RPDB003 is the invariant ID for "incompatible type change"
// (docs/invariants.md).
const RPDB003 = "RP-DB-003"

// evaluateTypeCompatibility implements RP-DB-003 (docs/invariants.md): a
// column type change must not narrow or otherwise incompatibly change a
// column while any live version reads or writes it under assumptions
// valid only for the old type. Widening changes (docs/invariants.md's
// fixed compatibility table, typecompat.go) are exempted from the
// reader/writer check entirely; Narrowing and Incomparable are treated
// identically — an unrecognized type pair fails toward "check it," the
// documented asymmetry from RP-DB-001/002's missing-evidence-is-UNKNOWN
// default (docs/invariants.md RP-DB-003 Limitations).
func evaluateTypeCompatibility(g *graph.Graph, services map[ir.ServiceKey]ir.Service) ir.Diagnostic {
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
			if cop.Op.Kind != ir.OpAlterColumnType {
				continue
			}
			target := cop.Op.TargetColumn()
			if cop.PriorType == "" {
				gaps.add(ir.EvidenceGap{
					Field:  fmt.Sprintf("%s prior type (before migration %q)", target, cop.MigrationID),
					Reason: "the column's type immediately before this operation could not be determined, so type-compatibility classification cannot run",
				})
				continue
			}
			if classifyTypeChange(cop.PriorType, cop.Op.NewType) == typeWidening {
				continue
			}
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
				if svc.ReadsColumn(target) || svc.WritesColumn(target) {
					violations = append(violations, violation{stateID: s.ID, op: cop, svcVersion: lv, svc: svc, liveInState: s.Live})
				}
			}
		}
	}

	if len(violations) == 0 {
		if gaps.len() > 0 {
			return ir.Diagnostic{
				InvariantID:     RPDB003,
				Verdict:         ir.VerdictUnknown,
				Summary:         fmt.Sprintf("%s: insufficient evidence to evaluate every reachable state", RPDB003),
				MissingEvidence: gaps.list(),
			}
		}
		return ir.Diagnostic{
			InvariantID: RPDB003,
			Verdict:     ir.VerdictSafe,
			Summary:     fmt.Sprintf("%s: no reachable state violates this invariant", RPDB003),
		}
	}

	best := selectShortest(g, violations)
	path := g.ShortestPath(best.stateID)
	target := best.op.Op.TargetColumn()
	compat := classifyTypeChange(best.op.PriorType, best.op.Op.NewType)
	compatLabel := "an incompatible (unrecognized) type pair"
	if compat == typeNarrowing {
		compatLabel = "a narrowing"
	}

	summary := fmt.Sprintf(
		"migration %q changes %s from %s to %s (%s type change) while %s@%s still reads or writes it",
		best.op.MigrationID, target, best.op.PriorType, best.op.Op.NewType, compatLabel, best.svcVersion.ServiceName, best.svcVersion.Version)

	evidence := []ir.Evidence{
		{
			Kind:        ir.EvidenceMigrationOp,
			Artifact:    best.op.MigrationID,
			Locator:     best.op.Op.Kind.String(),
			Line:        best.op.Op.SourceLine,
			Description: fmt.Sprintf("migration changes %s from %s to %s (%s)", target, best.op.PriorType, best.op.Op.NewType, compatLabel),
		},
		{
			Kind:        ir.EvidenceSchemaRead,
			Artifact:    best.svc.SourceFile,
			Description: fmt.Sprintf("%s@%s declares schema access to %s", best.svcVersion.ServiceName, best.svcVersion.Version, target),
		},
		{
			Kind:        ir.EvidenceRolloutStrategy,
			Description: describeReachability(best.liveInState),
		},
	}

	return ir.Diagnostic{
		InvariantID: RPDB003,
		Verdict:     ir.VerdictUnsafe,
		Summary:     summary,
		Evidence:    evidence,
		Counterexample: &ir.Counterexample{
			Path:           path,
			ViolatingState: g.State(best.stateID),
			RecommendedSequence: []string{
				fmt.Sprintf("deploy a compatibility release of %s that tolerates both %s and %s", best.svcVersion.ServiceName, best.op.PriorType, best.op.Op.NewType),
				"wait for the compatibility release to complete its rollout",
				fmt.Sprintf("verify no live version assumes %s", best.op.PriorType),
				fmt.Sprintf("apply migration %q", best.op.MigrationID),
			},
		},
		// RollbackVerdict left at its zero value: type-change rollback
		// risk is evaluated by RP-ROLLBACK-002 against the rollback's own
		// transition graph (rollback_plan.go), not derived here.
	}
}
