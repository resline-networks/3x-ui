package job

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// scriptedOnlineUsers answers one round per scan (the last round repeats), so a
// test can move the core's cumulative byte counters between scans.
type scriptedOnlineUsers struct {
	rounds [][]xray.OnlineUser
	calls  int
}

func (s *scriptedOnlineUsers) GetOnlineUsers() ([]xray.OnlineUser, bool, error) {
	round := min(s.calls, len(s.rounds)-1)
	s.calls++
	return s.rounds[round], true, nil
}

func n64(v int64) *int64 { return &v }

// counted is an IP the resline core counts: cumulative bytes since the core
// created the (user, IP) entry.
func counted(ip string, lastSeen, up, down int64) xray.OnlineIP {
	return xray.OnlineIP{IP: ip, LastSeen: lastSeen, Uplink: n64(up), Downlink: n64(down)}
}

type reportedBytes struct {
	up, down int64
	missing  bool
}

// reportedTraffic reads, for one email, what the clientIpsByGuid endpoint
// serves: ip -> bytes, across every guid subtree.
func reportedTraffic(t *testing.T, email string) map[string]reportedBytes {
	t.Helper()
	trees, err := (&service.InboundService{}).GetClientIpsByGuid()
	if err != nil {
		t.Fatalf("GetClientIpsByGuid: %v", err)
	}
	out := map[string]reportedBytes{}
	for _, perEmail := range trees {
		for _, e := range perEmail[email] {
			if e.Uplink == nil || e.Downlink == nil {
				out[e.IP] = reportedBytes{missing: true}
				continue
			}
			out[e.IP] = reportedBytes{up: *e.Uplink, down: *e.Downlink}
		}
	}
	return out
}

func wantBytes(t *testing.T, label string, got map[string]reportedBytes, ip string, up, down int64) {
	t.Helper()
	b, ok := got[ip]
	if !ok || b.missing {
		t.Fatalf("%s: %s bytes missing, got %+v", label, ip, got)
	}
	if b.up != up || b.down != down {
		t.Fatalf("%s: %s up=%d down=%d, want up=%d down=%d", label, ip, b.up, b.down, up, down)
	}
}

// Cumulative core counters become stored deltas: the first scan of a panel process is a baseline (like
// GetTraffic), later scans add the difference, and a backwards counter is all new traffic.
func TestIPTraffic_CumulativeCountersBecomeAccumulatedDeltas(t *testing.T) {
	setupIntegrationDB(t)
	t.Setenv("XUI_ENABLE_FAIL2BAN", "false")

	const email = "ip-bytes"
	const ip = "203.0.113.10"
	seedLinkedInboundWithClient(t, "inbound-ip-bytes", email, 0)

	seen := time.Now().Unix() - 60
	src := &scriptedOnlineUsers{rounds: [][]xray.OnlineUser{
		{{Email: email, IPs: []xray.OnlineIP{counted(ip, seen, 1000, 5000)}}},
		{{Email: email, IPs: []xray.OnlineIP{counted(ip, seen, 1500, 9000)}}},
		{{Email: email, IPs: []xray.OnlineIP{counted(ip, seen, 200, 300)}}}, // core restarted
		{{Email: email, IPs: []xray.OnlineIP{counted(ip, seen, 250, 400)}}},
	}}
	j := NewCheckClientIpJob()
	j.onlineUsers = src

	j.Run()
	wantBytes(t, "baseline scan", reportedTraffic(t, email), ip, 0, 0)
	j.Run()
	wantBytes(t, "second scan", reportedTraffic(t, email), ip, 500, 4000)
	j.Run()
	wantBytes(t, "after core restart", reportedTraffic(t, email), ip, 700, 4300)
	j.Run()
	wantBytes(t, "after restart, next scan", reportedTraffic(t, email), ip, 750, 4400)
}

// An IP closed since the last scan sits in the core's offline list: its bytes count (even for a user
// whose only connection opened and closed between scans) but it never becomes an observation.
func TestIPTraffic_OfflineIPsCountBytesButNeverObservations(t *testing.T) {
	setupIntegrationDB(t)
	t.Setenv("XUI_ENABLE_FAIL2BAN", "false")

	const alice, bob = "ip-bytes-alice", "ip-bytes-bob"
	seedLinkedInboundWithClient(t, "inbound-ip-bytes-a", alice, 0)
	seedLinkedInboundWithClient(t, "inbound-ip-bytes-b", bob, 0)

	now := time.Now().Unix()
	src := &scriptedOnlineUsers{rounds: [][]xray.OnlineUser{
		{{Email: alice, IPs: []xray.OnlineIP{
			counted("203.0.113.20", now-30, 100, 100),
			counted("203.0.113.21", now-20, 10, 10),
		}}},
		{
			{
				Email:      alice,
				IPs:        []xray.OnlineIP{counted("203.0.113.21", now-20, 15, 20)},
				OfflineIPs: []xray.OnlineIP{counted("203.0.113.20", now-30, 300, 500)},
			},
			// bob connected and left between the two scans.
			{Email: bob, OfflineIPs: []xray.OnlineIP{counted("198.51.100.30", now-5, 40, 4000)}},
		},
	}}
	j := NewCheckClientIpJob()
	j.onlineUsers = src
	j.Run()
	j.Run()

	got := reportedTraffic(t, alice)
	wantBytes(t, "closed connection", got, "203.0.113.20", 200, 400)
	wantBytes(t, "live connection", got, "203.0.113.21", 5, 10)
	wantBytes(t, "open-and-closed between scans", reportedTraffic(t, bob), "198.51.100.30", 40, 4000)

	if flat := ipSet(readClientIps(t, alice)); len(flat) != 2 {
		t.Fatalf("alice's flat blob must hold exactly what live scans saw, got %v", flat)
	}
	var bobRows int64
	database.GetDB().Model(&model.InboundClientIps{}).Where("client_email = ?", bob).Count(&bobRows)
	if bobRows != 0 {
		t.Fatalf("an offline-only user must not enter the flat blob, got %d rows", bobRows)
	}
	for _, e := range readClientIps(t, alice) {
		if e.Uplink != nil || e.Downlink != nil {
			t.Fatalf("the flat blob must never carry bytes, got %+v", e)
		}
	}
}

// An upstream core reports no counters: entries keep working and carry no
// bytes at all, which is "unknown", never zero.
func TestIPTraffic_UncountedIPsReportNoBytes(t *testing.T) {
	setupIntegrationDB(t)
	t.Setenv("XUI_ENABLE_FAIL2BAN", "false")

	const email = "ip-bytes-upstream"
	seedLinkedInboundWithClient(t, "inbound-ip-bytes-up", email, 0)

	j := NewCheckClientIpJob()
	j.onlineUsers = staticOnlineUsers{{Email: email, IPs: []xray.OnlineIP{
		{IP: "203.0.113.40", LastSeen: time.Now().Unix()},
	}}}
	j.Run()
	j.Run()

	got := reportedTraffic(t, email)
	if b, ok := got["203.0.113.40"]; !ok || !b.missing {
		t.Fatalf("an uncounted IP must be reported without bytes, got %+v", got)
	}
}

// banRun is one LimitIP sequence's observable outcome: what fail2ban was fed
// (minus the wall-clock prefix) and what the flat blob kept.
type banRun struct {
	banLines []string
	flat     map[string]int64
}

func runBanSequence(t *testing.T, rounds [][]xray.OnlineUser, email string, limit int) banRun {
	t.Helper()
	setupIntegrationDB(t)
	t.Setenv("XUI_ENABLE_FAIL2BAN", "true")
	fakeFail2BanClient(t)
	seedLinkedInboundWithClient(t, "inbound-"+email, email, limit)

	j := NewCheckClientIpJob()
	j.onlineUsers = &scriptedOnlineUsers{rounds: rounds}
	for range rounds {
		j.Run()
	}
	j.Run()

	body, _ := os.ReadFile(readIpLimitLogPath())
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if i := strings.Index(line, "[LIMIT_IP]"); i >= 0 {
			lines = append(lines, line[i:])
		}
	}
	return banRun{banLines: lines, flat: ipSet(readClientIps(t, email))}
}

// stripBytes is the same core answer as an upstream core would give it: no
// counters and no offline list.
func stripBytes(rounds [][]xray.OnlineUser) [][]xray.OnlineUser {
	out := make([][]xray.OnlineUser, len(rounds))
	for r, users := range rounds {
		for _, u := range users {
			ips := make([]xray.OnlineIP, 0, len(u.IPs))
			for _, ip := range u.IPs {
				ips = append(ips, xray.OnlineIP{IP: ip.IP, LastSeen: ip.LastSeen})
			}
			if len(ips) > 0 {
				out[r] = append(out[r], xray.OnlineUser{Email: u.Email, IPs: ips})
			}
		}
	}
	return out
}

// LimitIP / fail2ban decide exactly as before (2026-08-17 mis-ban): an over-limit pair and a just-closed
// IP give the same ban lines and flat blob with or without per-IP bytes and offline IPs.
func TestIPTraffic_LimitIPDecisionsUnchangedByBytesAndOfflineIPs(t *testing.T) {
	now := time.Now().Unix()
	oldDispatch, newDispatch := now-3600, now-10
	rounds := [][]xray.OnlineUser{
		{{Email: "limit-bytes", IPs: []xray.OnlineIP{
			counted("198.51.100.1", oldDispatch, 10, 10),
			counted("198.51.100.2", newDispatch, 10, 10),
		}}},
		{{
			Email: "limit-bytes",
			IPs: []xray.OnlineIP{
				counted("198.51.100.1", oldDispatch, 20, 20),
				counted("198.51.100.2", newDispatch, 20, 20),
			},
			OfflineIPs: []xray.OnlineIP{counted("198.51.100.3", now-5, 7, 7)},
		}},
		{{
			Email: "limit-bytes",
			IPs: []xray.OnlineIP{
				counted("198.51.100.1", oldDispatch, 30, 30),
				counted("198.51.100.2", newDispatch, 30, 30),
			},
			OfflineIPs: []xray.OnlineIP{counted("198.51.100.3", now-5, 9, 9)},
		}},
	}

	withBytes := runBanSequence(t, rounds, "limit-bytes", 1)
	without := runBanSequence(t, stripBytes(rounds), "limit-bytes", 1)

	if len(without.banLines) != 1 || !strings.Contains(without.banLines[0], "198.51.100.1") {
		t.Fatalf("reference run must ban the old IP exactly once, got %q", without.banLines)
	}
	if strings.Join(withBytes.banLines, "\n") != strings.Join(without.banLines, "\n") {
		t.Fatalf("ban lines changed with bytes:\n with: %q\n without: %q", withBytes.banLines, without.banLines)
	}
	if len(withBytes.flat) != len(without.flat) {
		t.Fatalf("flat blob changed with bytes: with %v, without %v", withBytes.flat, without.flat)
	}
	for ip, ts := range without.flat {
		if withBytes.flat[ip] != ts {
			t.Fatalf("flat blob changed with bytes: with %v, without %v", withBytes.flat, without.flat)
		}
	}
	if _, ok := withBytes.flat["198.51.100.3"]; ok {
		t.Fatalf("an offline IP must never enter the flat blob, got %v", withBytes.flat)
	}
}

// One live IP within the limit plus a just-closed IP: the closed one is not a
// connection, so nothing is banned even though the limit is 1.
func TestIPTraffic_OfflineIPDoesNotCountTowardLimit(t *testing.T) {
	now := time.Now().Unix()
	got := runBanSequence(t, [][]xray.OnlineUser{
		{{
			Email:      "limit-offline",
			IPs:        []xray.OnlineIP{counted("192.0.2.1", now-10, 1, 1)},
			OfflineIPs: []xray.OnlineIP{counted("192.0.2.2", now-2, 1, 1)},
		}},
		{{
			Email:      "limit-offline",
			IPs:        []xray.OnlineIP{counted("192.0.2.1", now-10, 2, 2)},
			OfflineIPs: []xray.OnlineIP{counted("192.0.2.2", now-2, 5, 5)},
		}},
	}, "limit-offline", 1)
	if len(got.banLines) != 0 {
		t.Fatalf("an offline IP must not trigger a ban, got %q", got.banLines)
	}
	if _, ok := got.flat["192.0.2.2"]; ok || len(got.flat) != 1 {
		t.Fatalf("flat blob must hold only the live IP, got %v", got.flat)
	}
}
