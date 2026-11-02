package version

import "testing"

func TestCompareRejectsUnexpectedFormats(t *testing.T) {
	if _, ok := Compare("latest", "2.9.3"); ok {
		t.Fatal("expected non-semver latest tag to be rejected")
	}
	if _, ok := Compare("v2.9", "2.9.3"); ok {
		t.Fatal("expected short version to be rejected")
	}
}

// The resline fork tags its releases <upstream>-resline.<n>. A plain upstream
// version counts as resline.0 of the same base, so the fork build of a release
// is newer than the upstream build of it, and a newer upstream base still wins.
func TestCompareResline(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v3.11.0-resline.2", "3.11.0-resline.1", 1},
		{"3.11.0-resline.1", "v3.11.0-resline.1", 0},
		{"v3.11.0-resline.1", "3.11.0-resline.10", -1},
		{"v3.11.0-resline.1", "3.11.0", 1},
		{"3.11.0", "v3.11.0-resline.1", -1},
		{"v3.12.0-resline.1", "3.11.0-resline.9", 1},
		{"v3.11.0", "3.10.2-resline.3", 1},
	}
	for _, tc := range cases {
		got, ok := Compare(tc.a, tc.b)
		if !ok || got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, %v; want %d, true", tc.a, tc.b, got, ok, tc.want)
		}
	}
}

func TestCompareRejectsForeignSuffixes(t *testing.T) {
	for _, v := range []string{"3.11.0-rc1", "3.11.0-resline", "3.11.0-resline.x", "3.11.0-resline.1.2", "3.11.0-resline.-1", "dev+1a2b3c4d"} {
		if _, ok := Compare(v, "3.11.0"); ok {
			t.Errorf("Compare(%q, ...) accepted an unknown version shape", v)
		}
	}
}
