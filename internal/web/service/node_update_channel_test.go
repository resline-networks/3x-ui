package service

import (
	"strings"
	"testing"
)

// A master asked to move nodes to the dev channel refuses before contacting
// any node: the resline fork publishes no dev builds.
func TestUpdatePanelsRefusesDevChannel(t *testing.T) {
	results, err := (&NodeService{}).UpdatePanels([]int{1}, true)
	if err == nil || !strings.Contains(err.Error(), "dev channel") {
		t.Fatalf("UpdatePanels(dev=true) err = %v, want a dev channel refusal", err)
	}
	if results != nil {
		t.Fatalf("UpdatePanels(dev=true) results = %v, want none", results)
	}
}
