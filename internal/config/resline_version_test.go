package config

import (
	"strings"
	"testing"

	versionutil "github.com/mhsanaei/3x-ui/v3/internal/util/version"
)

// `x-ui -v` prints the embedded version. The resline fork's drift check
// treats a panel without the resline marker as a plain upstream install, so
// the embedded version must carry the marker and still parse as a version.
func TestBaseVersionCarriesReslineMarker(t *testing.T) {
	v := GetBaseVersion()
	if !strings.Contains(v, versionutil.ReslineSuffix) {
		t.Fatalf("embedded version %q has no %q marker", v, versionutil.ReslineSuffix)
	}
	if _, ok := versionutil.Compare(v, v); !ok {
		t.Fatalf("embedded version %q does not parse", v)
	}
}
