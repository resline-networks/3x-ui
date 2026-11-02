package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func b64(v int64) *int64 { return &v }

func withBytes(ip string, ts, up, down int64) model.ClientIpEntry {
	return model.ClientIpEntry{IP: ip, Timestamp: ts, Uplink: b64(up), Downlink: b64(down)}
}

func byGuidEntry(t *testing.T, guid, email, ip string) model.ClientIpEntry {
	t.Helper()
	trees, err := (&InboundService{}).GetClientIpsByGuid()
	if err != nil {
		t.Fatalf("GetClientIpsByGuid: %v", err)
	}
	for _, e := range trees[guid][email] {
		if e.IP == ip {
			return e
		}
	}
	t.Fatalf("%s/%s/%s not reported, got %v", guid, email, ip, trees)
	return model.ClientIpEntry{}
}

func assertEntryBytes(t *testing.T, label string, e model.ClientIpEntry, up, down int64) {
	t.Helper()
	if e.Uplink == nil || e.Downlink == nil {
		t.Fatalf("%s: %s bytes missing: %+v", label, e.IP, e)
	}
	if *e.Uplink != up || *e.Downlink != down {
		t.Fatalf("%s: %s up=%d down=%d, want up=%d down=%d", label, e.IP, *e.Uplink, *e.Downlink, up, down)
	}
}

// The scanner hands this panel's own observations over as per-scan deltas;
// they add up on the stored entry.
func TestIPTraffic_LocalDeltasAccumulate(t *testing.T) {
	setupClientIpTestDB(t)
	now := time.Now().Unix()
	svc := &InboundService{}

	for _, d := range []int64{0, 100, 250} {
		if err := svc.RecordLocalClientIps("guid-self", map[string][]model.ClientIpEntry{
			"u@x": {withBytes("1.1.1.1", now, d, 2*d)},
		}); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	assertEntryBytes(t, "local", byGuidEntry(t, "guid-self", "u@x", "1.1.1.1"), 350, 700)

	// A scan without counters (core swapped for an upstream one) keeps the total.
	if err := svc.RecordLocalClientIps("guid-self", map[string][]model.ClientIpEntry{
		"u@x": {{IP: "1.1.1.1", Timestamp: now}},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	assertEntryBytes(t, "after uncounted scan", byGuidEntry(t, "guid-self", "u@x", "1.1.1.1"), 350, 700)
}

// A node reports cumulative totals each sync: the master stores them as-is (repeats do not add up),
// a lower value is the node reset passed through, and a report without bytes leaves totals alone.
func TestIPTraffic_NodeReportReplacesTotals(t *testing.T) {
	setupClientIpTestDB(t)
	now := time.Now().Unix()
	svc := &InboundService{}
	report := func(e model.ClientIpEntry) {
		t.Helper()
		if err := svc.MergeClientIpsByGuid(nil, map[string]map[string][]model.ClientIpEntry{
			"guid-node": {"u@x": {e}},
		}); err != nil {
			t.Fatalf("merge: %v", err)
		}
	}

	report(withBytes("2.2.2.2", now, 500, 900))
	report(withBytes("2.2.2.2", now, 500, 900))
	assertEntryBytes(t, "repeated report", byGuidEntry(t, "guid-node", "u@x", "2.2.2.2"), 500, 900)

	report(withBytes("2.2.2.2", now, 40, 60))
	assertEntryBytes(t, "node reset", byGuidEntry(t, "guid-node", "u@x", "2.2.2.2"), 40, 60)

	report(model.ClientIpEntry{IP: "2.2.2.2", Timestamp: now})
	assertEntryBytes(t, "report without bytes", byGuidEntry(t, "guid-node", "u@x", "2.2.2.2"), 40, 60)
}

// GET /server/clientIps sums every node's (and this panel's) bytes per client and IP; an IP no node
// counted carries no bytes, and the stored blob itself never holds bytes.
func TestIPTraffic_FlatViewSumsNodesAndStoresNoBytes(t *testing.T) {
	setupClientIpTestDB(t)
	now := time.Now().Unix()
	svc := &InboundService{}

	// A node's flat view arrives with bytes on it; the IP-limit blob drops them.
	if err := svc.MergeInboundClientIps([]model.InboundClientIps{{
		ClientEmail: "u@x",
		Ips:         mustEntriesJSON(t, withBytes("3.3.3.3", now, 1, 1), model.ClientIpEntry{IP: "4.4.4.4", Timestamp: now}),
	}}); err != nil {
		t.Fatalf("merge flat: %v", err)
	}
	var row model.InboundClientIps
	if err := database.GetDB().Where("client_email = ?", "u@x").First(&row).Error; err != nil {
		t.Fatalf("read flat: %v", err)
	}
	var stored []model.ClientIpEntry
	_ = json.Unmarshal([]byte(row.Ips), &stored)
	for _, e := range stored {
		if e.Uplink != nil || e.Downlink != nil {
			t.Fatalf("flat blob must not store bytes, got %s", row.Ips)
		}
	}

	if err := svc.RecordLocalClientIps("guid-self", map[string][]model.ClientIpEntry{
		"u@x": {withBytes("3.3.3.3", now, 10, 20)},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := svc.MergeClientIpsByGuid(nil, map[string]map[string][]model.ClientIpEntry{
		"guid-a": {"u@x": {withBytes("3.3.3.3", now, 100, 200)}},
		"guid-b": {"u@x": {withBytes("3.3.3.3", now, 1000, 2000)}, "other@x": {withBytes("3.3.3.3", now, 7, 7)}},
	}); err != nil {
		t.Fatalf("merge byGuid: %v", err)
	}

	rows, err := svc.GetAllInboundClientIpsWithTraffic()
	if err != nil {
		t.Fatalf("GetAllInboundClientIpsWithTraffic: %v", err)
	}
	if len(rows) != 1 || rows[0].ClientEmail != "u@x" {
		t.Fatalf("rows = %+v", rows)
	}
	var entries []model.ClientIpEntry
	if err := json.Unmarshal([]byte(rows[0].Ips), &entries); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := map[string]model.ClientIpEntry{}
	for _, e := range entries {
		got[e.IP] = e
	}
	assertEntryBytes(t, "cluster sum", got["3.3.3.3"], 1110, 2220)
	if e := got["4.4.4.4"]; e.IP == "" || e.Uplink != nil || e.Downlink != nil {
		t.Fatalf("an IP no node counted must carry no bytes, got %+v", e)
	}
	if got["3.3.3.3"].Timestamp != now {
		t.Fatalf("timestamps must come from the flat blob unchanged, got %+v", got["3.3.3.3"])
	}
}

func mustEntriesJSON(t *testing.T, entries ...model.ClientIpEntry) string {
	t.Helper()
	b, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
