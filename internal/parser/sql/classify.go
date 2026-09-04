package sql

import "github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"

// Destructiveness/Reversibility classification is derived here, once, at
// parse time (docs/architecture.md §2.4) — the invariant engine and
// transition graph never re-derive it from an operation's raw fields.

// classifyAddColumn implements docs/invariants.md RP-DB-004's safe/unsafe
// split: a column added NOT NULL with no DEFAULT can reject an old
// writer's row that omits it; a nullable column, or one with a DEFAULT,
// cannot.
func classifyAddColumn(op *ir.MigrationOp) {
	if !op.Nullable && op.Default == nil {
		op.Destructiveness = ir.ConditionallyDestructive
	} else {
		op.Destructiveness = ir.NonDestructive
	}
	// The column did not exist before this operation, so no live version
	// could have depended on it; dropping it back out is a clean,
	// lossless inverse.
	op.Reversibility = ir.Reversible
}

// classifyDropColumn implements RP-DB-001/002: dropping a column always
// risks a live reader or writer that still depends on it, and the data
// in the column is gone regardless of whether any version depended on it.
func classifyDropColumn(op *ir.MigrationOp) {
	op.Destructiveness = ir.Destructive
	op.Reversibility = ir.Irreversible
}

// classifyRenameColumn implements RP-DB-005: a bare rename is treated as
// a simultaneous drop-of-old-name for safety-evaluation purposes (any
// live version still referencing the old name breaks identically to a
// drop), but unlike a real drop, no data is lost — renaming back is an
// exact, lossless inverse.
func classifyRenameColumn(op *ir.MigrationOp) {
	op.Destructiveness = ir.Destructive
	op.Reversibility = ir.Reversible
}

// classifyAlterColumnType is intentionally conservative: docs/invariants.md
// RP-DB-003 (the widening/narrowing Postgres type-compatibility table) is
// not yet implemented, so V1 cannot distinguish a safe widening
// (integer -> bigint) from an unsafe narrowing. Per docs/architecture.md
// §2.4's documented default, an unclassifiable type change fails toward
// "assume it could be destructive," not toward SAFE — this will be
// refined, not loosened, once RP-DB-003 ships.
func classifyAlterColumnType(op *ir.MigrationOp) {
	op.Destructiveness = ir.ConditionallyDestructive
	op.Reversibility = ir.ConditionallyReversible
}

// classifySetNotNull implements RP-DB-004: tightening a constraint can
// reject an old writer's row (conditionally destructive), but the
// reverse operation (DROP NOT NULL) is a structural loosening that always
// succeeds — so SET NOT NULL is itself cleanly reversible.
func classifySetNotNull(op *ir.MigrationOp) {
	op.Destructiveness = ir.ConditionallyDestructive
	op.Reversibility = ir.Reversible
}

// classifyDropNotNull is the asymmetric counterpart to classifySetNotNull:
// loosening a constraint never rejects a write or loses data (so it is
// NonDestructive), but undoing it (re-imposing NOT NULL) can fail if any
// NULL was written in the interim — a fact only knowable at evaluation
// time against actual data, not at parse time — so it is only
// ConditionallyReversible.
func classifyDropNotNull(op *ir.MigrationOp) {
	op.Destructiveness = ir.NonDestructive
	op.Reversibility = ir.ConditionallyReversible
}
