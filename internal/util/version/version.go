// Package version compares 3x-ui release versions ("v3.8.0" or "3.8.0", and
// the resline fork's "v3.11.0-resline.1").
package version

import (
	"strconv"
	"strings"
)

// Compare returns -1, 0 or 1 as a is older than, equal to or newer than b, and
// false when either side is not a MAJOR.MINOR.PATCH[-resline.N] version.
func Compare(a, b string) (int, bool) {
	aParts, okA := parse(a)
	bParts, okB := parse(b)
	if !okA || !okB {
		return 0, false
	}
	for i := range len(aParts) {
		if aParts[i] > bParts[i] {
			return 1, true
		}
		if aParts[i] < bParts[i] {
			return -1, true
		}
	}
	return 0, true
}

// Normalize strips surrounding space and a leading "v" from a version tag.
func Normalize(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// ReslineSuffix separates the upstream version from the resline fork's own
// release counter: v3.11.0-resline.2 is the fork's second release on v3.11.0.
const ReslineSuffix = "-resline."

// parse reads MAJOR.MINOR.PATCH[-resline.N]; plain upstream is resline.0 of its base, so it sorts
// just below the fork's first release of that base. Any other suffix is rejected.
func parse(v string) ([4]int, bool) {
	var result [4]int
	base := Normalize(v)
	if i := strings.Index(base, ReslineSuffix); i >= 0 {
		n, err := strconv.Atoi(base[i+len(ReslineSuffix):])
		if err != nil || n < 1 {
			return result, false
		}
		result[3] = n
		base = base[:i]
	}
	parts := strings.Split(base, ".")
	if len(parts) != 3 {
		return result, false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return result, false
		}
		result[i] = n
	}
	return result, true
}
