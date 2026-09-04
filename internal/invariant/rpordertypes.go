package invariant

import (
	"strconv"
	"strings"
)

// compareVersions compares two version strings under a best-effort
// dotted-numeric scheme ("v1", "v1.2", "1.2.3", ...): each dot-separated
// segment (after stripping a leading "v"/"V") must parse as a
// non-negative integer for the comparison to be meaningful at all.
// ok=false means the pair is not comparable under this scheme — the
// caller's UNKNOWN case, not an assumed order.
//
// docs/architecture.md §2.1.1 describes a project-declared version
// scheme (semver / opaque-ordered / opaque-unordered) sourced from
// project config; V1 has no internal/config package yet (see
// docs/architecture.md §7's own "not yet implemented" note elsewhere in
// this codebase), so RP-ORDER falls back to this single, self-contained
// scheme rather than guessing a project-wide policy that doesn't exist
// yet. A version pair outside this scheme (e.g. build hashes, opaque
// tags) is honestly UNKNOWN, matching the documented opaque-unordered
// fallback's spirit even without the config to name it explicitly.
func compareVersions(a, b string) (cmp int, ok bool) {
	av, aok := parseVersionOrdinal(a)
	bv, bok := parseVersionOrdinal(b)
	if !aok || !bok {
		return 0, false
	}
	n := len(av)
	if len(bv) > n {
		n = len(bv)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(av) {
			x = av[i]
		}
		if i < len(bv) {
			y = bv[i]
		}
		if x != y {
			if x < y {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

func parseVersionOrdinal(s string) ([]int, bool) {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	if s == "" {
		return nil, false
	}
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}
