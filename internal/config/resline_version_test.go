package config

import (
	"strings"
	"testing"

	versionutil "github.com/mhsanaei/3x-ui/v3/internal/util/version"
)

// The drift check treats a panel whose `x-ui -v` lacks the resline marker as upstream,
// so the embedded version must carry the marker and still parse as a version.
func TestBaseVersionCarriesReslineMarker(t *testing.T) {
	v := GetBaseVersion()
	if !strings.Contains(v, versionutil.ReslineSuffix) {
		t.Fatalf("embedded version %q has no %q marker", v, versionutil.ReslineSuffix)
	}
	if _, ok := versionutil.Compare(v, v); !ok {
		t.Fatalf("embedded version %q does not parse", v)
	}
}
