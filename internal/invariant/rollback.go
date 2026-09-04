package invariant

import "github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"

// EvaluateRollback classifies the safety of restoring rollbackTarget
// against the schema resulting from committed, given the same
// destructive-column-removal facts RP-DB-001/002 already evaluate
// (docs/architecture.md §5). This is not a boolean "does a DOWN
// migration exist": it asks whether rollbackTarget's own declared
// SchemaReads/SchemaWrites depend on a column one of the committed
// operations removed, and if so, whether that removal's Reversibility
// makes recovering from the rollback even possible.
//
// Per docs/architecture.md §5, "migration rollback vs. operational
// rollback": this evaluates whether rollbackTarget can run correctly
// against the schema as committed, without assuming the migration itself
// gets reverted — restoring an old version does not, by itself, undo a
// schema change.
func EvaluateRollback(committed []ir.CommittedOp, rollbackTarget ir.Service) ir.RollbackVerdict {
	verdict := ir.RollbackSafe
	for _, cop := range committed {
		// A bare rename is, for rollback purposes, structurally identical
		// to a drop of the old column name (docs/invariants.md RP-DB-005):
		// a version restored against the current schema that still
		// references the pre-rename name fails exactly as it would if the
		// column had been dropped outright. TargetColumn() already
		// resolves to the old name for OpRenameColumn (ir/migration.go).
		if cop.Op.Kind != ir.OpDropColumn && cop.Op.Kind != ir.OpRenameColumn {
			continue
		}
		target := cop.Op.TargetColumn()

		if !rollbackTarget.TouchesTable(target.Table) {
			// No evidence either way for this table: we cannot rule out
			// a dependency, so this is not a clean SAFE contribution
			// (docs/vision.md §10) — but note it is milder than a
			// confirmed UNSAFE, so it still yields to one if a later op
			// in this same loop is confirmed unsafe.
			verdict = ir.CombineRollback(verdict, ir.RollbackUnknown)
			continue
		}
		if !rollbackTarget.ReadsColumn(target) && !rollbackTarget.WritesColumn(target) {
			// Positive evidence the rollback target does not depend on
			// the dropped column: this specific operation contributes
			// nothing unsafe.
			continue
		}

		switch cop.Op.Reversibility {
		case ir.Irreversible:
			verdict = ir.CombineRollback(verdict, ir.RollbackUnsafe)
		case ir.ConditionallyReversible, ir.Reversible:
			// Even a structurally Reversible drop (architecture.md §2.4
			// classifies none of DropColumn's reversibility as plain
			// Reversible today, but a future op kind might) still
			// requires an explicit migration-reversal action the
			// rollback plan hasn't necessarily taken — restoring the
			// old version alone does not restore the column
			// (docs/architecture.md §5, "operational rollback" does not
			// imply "migration rollback").
			verdict = ir.CombineRollback(verdict, ir.RollbackConditionallySafe)
		default:
			verdict = ir.CombineRollback(verdict, ir.RollbackUnknown)
		}
	}
	return verdict
}
