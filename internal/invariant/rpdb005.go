package invariant

import (
	"fmt"
	"sort"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// RPDB005 is the invariant ID for "rename without compatibility period"
// (docs/invariants.md).
const RPDB005 = "RP-DB-005"

// renameViolation mirrors violation (rpdb.go), plus which of
// reads/writes fired — a bare rename breaks a stale reference the same
// way whether it was a read or a write (the column no longer exists under
// that name), so, unlike RP-DB-001/002, this invariant checks both facts
// at once under a single ID (docs/invariants.md RP-DB-005) rather than
// splitting by access kind.
type renameViolation struct {
	stateID     int
	op          ir.CommittedOp
	svcVersion  ir.LiveVersion
	svc         ir.Service
	liveInState []ir.LiveVersion
	reads       bool
	writes      bool
}

// evaluateRenameWithoutCompatibility implements RP-DB-005
// (docs/invariants.md): treats a bare ALTER TABLE ... RENAME COLUMN as an
// implicit drop of the old column name (the same reclassification
// docs/invariants.md RP-DB-005 describes as "delegating" to the
// RP-DB-001/002 mechanism), reported under its own ID because the
// recommended remediation for a rename differs from a plain drop.
func evaluateRenameWithoutCompatibility(g *graph.Graph, services map[ir.ServiceKey]ir.Service) ir.Diagnostic {
	var violations []renameViolation
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
			if cop.Op.Kind != ir.OpRenameColumn {
				continue
			}
			oldName := cop.Op.TargetColumn() // TargetColumn resolves to the source column for a rename
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
				if !svc.TouchesTable(oldName.Table) {
					gaps.add(ir.EvidenceGap{
						Field:  fmt.Sprintf("Service %s@%s schema access on table %s", lv.ServiceName, lv.Version, oldName.Table),
						Reason: "contract metadata for this service version does not mention this table",
					})
					continue
				}
				reads, writes := svc.ReadsColumn(oldName), svc.WritesColumn(oldName)
				if reads || writes {
					violations = append(violations, renameViolation{
						stateID: s.ID, op: cop, svcVersion: lv, svc: svc, liveInState: s.Live,
						reads: reads, writes: writes,
					})
				}
			}
		}
	}

	if len(violations) == 0 {
		if gaps.len() > 0 {
			return ir.Diagnostic{
				InvariantID:     RPDB005,
				Verdict:         ir.VerdictUnknown,
				Summary:         fmt.Sprintf("%s: insufficient evidence to evaluate every reachable state", RPDB005),
				MissingEvidence: gaps.list(),
			}
		}
		return ir.Diagnostic{
			InvariantID: RPDB005,
			Verdict:     ir.VerdictSafe,
			Summary:     fmt.Sprintf("%s: no reachable state violates this invariant", RPDB005),
		}
	}

	ids := make([]int, len(violations))
	for i, v := range violations {
		ids[i] = v.stateID
	}
	best := violations[shortestByStateID(g, ids)]
	path := g.ShortestPath(best.stateID)
	oldName := best.op.Op.TargetColumn()
	newName := ir.ColumnRef{Table: best.op.Op.Table, Column: best.op.Op.NewColumn}
	accessLabel := accessLabel(best.reads, best.writes)

	summary := fmt.Sprintf(
		"migration %q renames %s to %s with no compatibility period, while %s@%s still %s the old name",
		best.op.MigrationID, oldName, newName.Column, best.svcVersion.ServiceName, best.svcVersion.Version, accessLabel)

	evidence := []ir.Evidence{
		{
			Kind:        ir.EvidenceMigrationOp,
			Artifact:    best.op.MigrationID,
			Locator:     best.op.Op.Kind.String(),
			Line:        best.op.Op.SourceLine,
			Description: fmt.Sprintf("migration renames %s to %s", oldName, newName.Column),
		},
	}
	if best.reads {
		evidence = append(evidence, ir.Evidence{
			Kind:        ir.EvidenceSchemaRead,
			Artifact:    best.svc.SourceFile,
			Description: fmt.Sprintf("%s@%s declares it reads %s", best.svcVersion.ServiceName, best.svcVersion.Version, oldName),
		})
	}
	if best.writes {
		evidence = append(evidence, ir.Evidence{
			Kind:        ir.EvidenceSchemaWrite,
			Artifact:    best.svc.SourceFile,
			Description: fmt.Sprintf("%s@%s declares it writes %s", best.svcVersion.ServiceName, best.svcVersion.Version, oldName),
		})
	}
	evidence = append(evidence, ir.Evidence{
		Kind:        ir.EvidenceRolloutStrategy,
		Description: describeReachability(best.liveInState),
	})

	rollbackVerdict := EvaluateRollback(g.State(best.stateID).SchemaState.CommittedOps, best.svc)

	return ir.Diagnostic{
		InvariantID: RPDB005,
		Verdict:     ir.VerdictUnsafe,
		Summary:     summary,
		Evidence:    evidence,
		Counterexample: &ir.Counterexample{
			Path:           path,
			ViolatingState: g.State(best.stateID),
			RecommendedSequence: []string{
				fmt.Sprintf("add %s as a new column alongside %s", newName, oldName),
				"dual-write both columns in application code and backfill the new column",
				fmt.Sprintf("deploy a release of %s that reads and writes only %s", best.svcVersion.ServiceName, newName),
				"wait for that release to complete its rollout",
				fmt.Sprintf("drop %s in a subsequent migration", oldName),
			},
		},
		RollbackVerdict: rollbackVerdict,
	}
}

func accessLabel(reads, writes bool) string {
	switch {
	case reads && writes:
		return "reads and writes"
	case writes:
		return "writes"
	default:
		return "reads"
	}
}
