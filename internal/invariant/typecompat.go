package invariant

import (
	"strconv"
	"strings"
)

// typeCompat classifies an OldType -> NewType column type change
// (docs/invariants.md RP-DB-003). The zero value, typeIncomparable, is
// deliberately the "unknown compatibility" case — a type pair this fixed
// table does not recognize must fail toward "could be unsafe," never
// toward Widening (docs/invariants.md RP-DB-003's Limitations: "a type
// pair not in the table is Incomparable by default... the safer failure
// direction than skipping the check entirely").
type typeCompat int

const (
	typeIncomparable typeCompat = iota
	typeWidening
	typeNarrowing
)

// typeInfo is a normalized Postgres column type: a family name plus a
// size dimension meaningful only within that family (integer/float rank,
// or varchar length; -1 means "unbounded", the widest possible value in
// that family).
type typeInfo struct {
	family string
	size   int
}

const unbounded = -1

// integerRank orders Postgres' fixed-width integer types by range, the
// only fact RP-DB-003 needs to know a widening (smallint -> bigint) from
// a narrowing (bigint -> smallint) integer change (docs/invariants.md
// RP-DB-003's unsafe example is the numeric-precision-loss analog of
// this).
var integerRank = map[string]int{
	"smallint": 1, "int2": 1,
	"integer": 2, "int": 2, "int4": 2,
	"bigint": 3, "int8": 3,
}

// floatRank orders Postgres' floating-point types by precision.
var floatRank = map[string]int{
	"real": 1, "float4": 1,
	"double precision": 2, "float8": 2,
}

// parseType normalizes a raw type string (as it appears verbatim in a
// migration or schema file, e.g. "varchar(255)", "NUMERIC", "BigInt")
// into a typeInfo. Returns ok=false for anything this fixed table does
// not recognize at all — classifyTypeChange treats that as
// typeIncomparable, not as a parse error to propagate (docs/architecture.md
// §2.4: this table is explicitly a bounded, versioned subset, not a
// general Postgres type parser).
func parseType(raw string) (typeInfo, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if r, ok := integerRank[s]; ok {
		return typeInfo{family: "integer", size: r}, true
	}
	if r, ok := floatRank[s]; ok {
		return typeInfo{family: "float", size: r}, true
	}
	if s == "text" {
		return typeInfo{family: "text"}, true
	}
	if n, ok := parseSized(s, "varchar"); ok {
		return typeInfo{family: "varchar", size: n}, true
	}
	if n, ok := parseSized(s, "character varying"); ok {
		return typeInfo{family: "varchar", size: n}, true
	}
	return typeInfo{}, false
}

// parseSized matches "<name>" (unbounded) or "<name>(N)" (bounded to N)
// against prefix, case-insensitively, returning N (or unbounded) and
// whether it matched at all.
func parseSized(s, prefix string) (int, bool) {
	if s == prefix {
		return unbounded, true
	}
	rest, ok := strings.CutPrefix(s, prefix+"(")
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutSuffix(rest, ")")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil {
		return 0, false
	}
	return n, true
}

// classifyTypeChange implements RP-DB-003's compatibility table
// (docs/invariants.md): Widening is safe for any old reader/writer of
// the old type; Narrowing or Incomparable is not (docs/invariants.md
// RP-DB-003's evaluation algorithm treats both identically — only
// Widening is exempted from the reader/writer check).
func classifyTypeChange(oldRaw, newRaw string) typeCompat {
	oldT, ok := parseType(oldRaw)
	if !ok {
		return typeIncomparable
	}
	newT, ok := parseType(newRaw)
	if !ok {
		return typeIncomparable
	}

	switch {
	case oldT.family == "integer" && newT.family == "integer":
		return compareRank(oldT.size, newT.size)
	case oldT.family == "float" && newT.family == "float":
		return compareRank(oldT.size, newT.size)
	case oldT.family == "varchar" && newT.family == "varchar":
		return compareBoundedSize(oldT.size, newT.size)
	case oldT.family == "varchar" && newT.family == "text":
		// text is unbounded: any varchar value already fits.
		return typeWidening
	case oldT.family == "text" && newT.family == "varchar":
		if newT.size == unbounded {
			return typeWidening
		}
		// Bounding a previously-unbounded column can truncate/reject an
		// existing or old-writer-supplied value.
		return typeNarrowing
	case oldT.family == "text" && newT.family == "text":
		return typeWidening
	default:
		return typeIncomparable
	}
}

func compareRank(oldRank, newRank int) typeCompat {
	if newRank >= oldRank {
		return typeWidening
	}
	return typeNarrowing
}

// compareBoundedSize compares two size limits where unbounded (-1) is
// wider than any bounded value.
func compareBoundedSize(oldSize, newSize int) typeCompat {
	if newSize == unbounded {
		return typeWidening
	}
	if oldSize == unbounded {
		return typeNarrowing
	}
	return compareRank(oldSize, newSize)
}
