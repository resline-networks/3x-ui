package service

import (
	"slices"
	"testing"
)

// "Switch Xray version" offers only resline builds of a supported base: a plain upstream tag in the
// fork would install an xray without the fork's changes.
func TestFilterXrayVersions(t *testing.T) {
	releases := []Release{
		{TagName: "v26.10.21-resline.2", Prerelease: true},
		{TagName: "v26.10.21-resline.1"},
		{TagName: "v26.10.21"},              // plain upstream tag: not offered
		{TagName: "v26.6.27-resline.1"},     // floor is inclusive
		{TagName: "v26.6.22-resline.4"},     // below the floor
		{TagName: "v26.10.21-rc1"},          // foreign suffix
		{TagName: "nightly"},                // not a version
		{TagName: "v27.1.0-resline.1"},      // newer major
		{TagName: "v26.10.21-resline.oops"}, // malformed counter
	}
	got := filterXrayVersions(releases)
	want := []string{"v26.10.21-resline.2", "v26.10.21-resline.1", "v26.6.27-resline.1", "v27.1.0-resline.1"}
	if !slices.Equal(got, want) {
		t.Fatalf("filterXrayVersions = %v, want %v", got, want)
	}
}
