package job

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// reportedByGuid reads what the clientIpsByGuid endpoint serves for one email:
// ip -> reported timestamp, across every guid subtree.
func reportedByGuid(t *testing.T, email string) map[string]int64 {
	t.Helper()
	trees, err := (&service.InboundService{}).GetClientIpsByGuid()
	if err != nil {
		t.Fatalf("GetClientIpsByGuid: %v", err)
	}
	out := map[string]int64{}
	for _, perEmail := range trees {
		for _, e := range perEmail[email] {
			if e.Timestamp > out[e.IP] {
				out[e.IP] = e.Timestamp
			}
		}
	}
	return out
}

// An idle long connection keeps its IP in xray's OnlineMap with the lastSeen of
// the dispatch that opened it, an hour ago. While it stays open, every scan
// must report the IP as present at scan time.
func TestPresence_IdleLongConnectionReportedAtScanTime(t *testing.T) {
	setupIntegrationDB(t)
	t.Setenv("XUI_ENABLE_FAIL2BAN", "false")

	const email = "idle-long-conn"
	seedLinkedInboundWithClient(t, "inbound-idle-long", email, 20)

	openedAt := time.Now().Unix() - 3600
	j := NewCheckClientIpJob()
	j.onlineUsers = staticOnlineUsers{{Email: email, IPs: []xray.OnlineIP{
		{IP: "203.0.113.50", LastSeen: openedAt},
	}}}

	scanStart := time.Now().Unix()
	j.Run()

	got := reportedByGuid(t, email)
	if ts, ok := got["203.0.113.50"]; !ok || ts < scanStart {
		t.Fatalf("idle long connection must be reported at scan time (>= %d), got %v", scanStart, got)
	}
}

// A long connection that keeps moving data looks exactly the same to the scan:
// OnlineMap.lastSeen only moves on a new dispatch, never on traffic. Several
// scans in a row must each report it at scan time.
func TestPresence_BusyLongConnectionReportedOnEveryScan(t *testing.T) {
	setupIntegrationDB(t)
	t.Setenv("XUI_ENABLE_FAIL2BAN", "false")

	const email = "busy-long-conn"
	seedLinkedInboundWithClient(t, "inbound-busy-long", email, 20)

	openedAt := time.Now().Unix() - 2*3600
	j := NewCheckClientIpJob()
	j.onlineUsers = staticOnlineUsers{{Email: email, IPs: []xray.OnlineIP{
		{IP: "203.0.113.60", LastSeen: openedAt},
	}}}

	for scan := range 3 {
		scanStart := time.Now().Unix()
		j.Run()
		if ts := reportedByGuid(t, email)["203.0.113.60"]; ts < scanStart {
			t.Fatalf("scan %d: busy long connection reported at %d, want >= %d", scan, ts, scanStart)
		}
	}
}

// Once the connection closes the IP leaves OnlineMap. The report keeps the
// last scan that saw it (no refresh) until it ages past the 30-minute window.
func TestPresence_ClosedConnectionKeptForGraceWindowThenDropped(t *testing.T) {
	setupIntegrationDB(t)
	t.Setenv("XUI_ENABLE_FAIL2BAN", "false")

	const email = "closed-conn"
	seedLinkedInboundWithClient(t, "inbound-closed", email, 20)
	guid, err := (&service.SettingService{}).GetPanelGuid()
	if err != nil || guid == "" {
		t.Fatalf("GetPanelGuid: %q, %v", guid, err)
	}

	now := time.Now().Unix()
	lastPresent := now - 5*60 // closed five minutes ago: inside the window
	longGone := now - 31*60   // closed 31 minutes ago: past the window
	blob, _ := model.MarshalClientIpEntries([]model.ClientIpEntry{
		{IP: "203.0.113.70", Timestamp: lastPresent},
		{IP: "203.0.113.71", Timestamp: longGone},
	})
	if err := database.GetDB().Create(&model.NodeClientIp{NodeGuid: guid, Email: email, Ips: blob}).Error; err != nil {
		t.Fatalf("seed attribution: %v", err)
	}

	// Neither IP has a connection open any more.
	j := NewCheckClientIpJob()
	j.onlineUsers = staticOnlineUsers{}
	j.Run()

	got := reportedByGuid(t, email)
	if ts, ok := got["203.0.113.70"]; !ok || ts != lastPresent {
		t.Fatalf("closed connection inside the window must keep its last presence %d, got %v", lastPresent, got)
	}
	if _, ok := got["203.0.113.71"]; ok {
		t.Fatalf("closed connection past the window must be dropped, got %v", got)
	}
}

// LimitIP / fail2ban must keep deciding on dispatch time, never on presence.
// Sequence: limit 1; an idle long connection opened an hour ago plus a new
// connection from a second IP, over three scans. Expected (last IP wins, a
// frozen dispatch time is published once): the old IP is banned exactly once,
// the new one is kept, and the flat clientIps blob still carries dispatch time.
func TestPresence_BanOutcomeFollowsDispatchTimeNotPresence(t *testing.T) {
	setupIntegrationDB(t)
	t.Setenv("XUI_ENABLE_FAIL2BAN", "true")
	fakeFail2BanClient(t)

	const email = "ban-sequence"
	seedLinkedInboundWithClient(t, "inbound-ban-seq", email, 1)

	now := time.Now().Unix()
	oldDispatch := now - 3600
	newDispatch := now - 10
	j := NewCheckClientIpJob()
	j.onlineUsers = staticOnlineUsers{{Email: email, IPs: []xray.OnlineIP{
		{IP: "198.51.100.1", LastSeen: oldDispatch},
		{IP: "198.51.100.2", LastSeen: newDispatch},
	}}}

	// The first scan only creates the row; the next two enforce.
	j.Run()
	j.Run()
	j.Run()

	body, _ := os.ReadFile(readIpLimitLogPath())
	log := string(body)
	if n := strings.Count(log, "Disconnecting OLD IP = 198.51.100.1 || Timestamp = "+strconv.FormatInt(oldDispatch, 10)); n != 1 {
		t.Fatalf("old IP must be banned exactly once at its dispatch time, got %d lines:\n%s", n, log)
	}
	if strings.Contains(log, "198.51.100.2") {
		t.Fatalf("newest IP must keep the slot, log:\n%s", log)
	}

	flat := ipSet(readClientIps(t, email))
	if ts, ok := flat["198.51.100.2"]; !ok || ts != newDispatch {
		t.Fatalf("flat clientIps must keep the dispatch time %d for the kept IP, got %v", newDispatch, flat)
	}
	if _, ok := flat["198.51.100.1"]; ok {
		t.Fatalf("banned IP must not stay in the flat blob, got %v", flat)
	}

	// Presence is still reported for both while their connections are open.
	byGuid := reportedByGuid(t, email)
	for _, ip := range []string{"198.51.100.1", "198.51.100.2"} {
		if byGuid[ip] < now {
			t.Fatalf("%s must be reported present at scan time, got %v", ip, byGuid)
		}
	}
}

// The cluster-wide count reads only the flat blob. A remote IP whose last
// dispatch is ten minutes old is historical there, even when attribution
// reports it present right now; counting it would ban the local live IP.
func TestPresence_FreshAttributionDoesNotCountTowardLimit(t *testing.T) {
	setupIntegrationDB(t)
	t.Setenv("XUI_ENABLE_FAIL2BAN", "true")
	fakeFail2BanClient(t)

	const email = "remote-idle"
	seedLinkedInboundWithClient(t, "inbound-remote-idle", email, 1)

	now := time.Now().Unix()
	seedClientIps(t, email, []model.ClientIpEntry{{IP: "192.0.2.80", Timestamp: now - 600}})
	blob, _ := model.MarshalClientIpEntries([]model.ClientIpEntry{{IP: "192.0.2.80", Timestamp: now}})
	if err := database.GetDB().Create(&model.NodeClientIp{NodeGuid: "remote-node-guid", Email: email, Ips: blob}).Error; err != nil {
		t.Fatalf("seed remote attribution: %v", err)
	}

	j := NewCheckClientIpJob()
	j.onlineUsers = staticOnlineUsers{{Email: email, IPs: []xray.OnlineIP{
		{IP: "192.0.2.81", LastSeen: now},
	}}}
	j.Run()
	j.Run()

	if info, err := os.Stat(readIpLimitLogPath()); err == nil && info.Size() > 0 {
		body, _ := os.ReadFile(readIpLimitLogPath())
		t.Fatalf("a remote IP with an old dispatch time must not count toward the limit, got:\n%s", body)
	}
}
