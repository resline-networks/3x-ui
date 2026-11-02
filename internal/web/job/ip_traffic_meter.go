package job

import (
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// ipTrafficDelta is what one (client, source IP) moved since the previous scan.
type ipTrafficDelta struct {
	ip       string
	lastSeen int64
	up, down int64
}

type ipTrafficKey struct{ email, ip string }

type ipCounters struct{ up, down int64 }

// ipTrafficMeter turns the core's cumulative per-(client, IP) byte counters
// into per-scan deltas, the same way GetTraffic treats the per-client counters:
// the first scan of a panel process only records baselines (the counters may
// hold traffic already accounted before a panel restart); after that an entry
// the meter has not seen counts from zero, and a counter that moved backwards
// means the core restarted or recreated the entry, so its new value is all new
// traffic. Baselines are kept only for entries the core still reports, so a
// recycled entry starts over and the map stays bounded by the core's own.
type ipTrafficMeter struct {
	last   map[ipTrafficKey]ipCounters
	primed bool
}

// measure returns email -> deltas for every IP the core counted, online or
// offline. IPs without counters (an upstream core) are left out entirely.
func (m *ipTrafficMeter) measure(users []xray.OnlineUser) map[string][]ipTrafficDelta {
	next := make(map[ipTrafficKey]ipCounters, len(m.last))
	out := make(map[string][]ipTrafficDelta)
	baseline := !m.primed
	for _, u := range users {
		for _, ips := range [][]xray.OnlineIP{u.IPs, u.OfflineIPs} {
			for _, e := range ips {
				if e.Uplink == nil || e.Downlink == nil {
					continue
				}
				key := ipTrafficKey{email: u.Email, ip: e.IP}
				if _, dup := next[key]; dup {
					continue
				}
				cur := ipCounters{up: *e.Uplink, down: *e.Downlink}
				next[key] = cur
				var d ipCounters
				prev, seen := m.last[key]
				switch {
				case baseline:
				case !seen, cur.up < prev.up || cur.down < prev.down:
					d = cur
				default:
					d = ipCounters{up: cur.up - prev.up, down: cur.down - prev.down}
				}
				out[u.Email] = append(out[u.Email], ipTrafficDelta{ip: e.IP, lastSeen: e.LastSeen, up: d.up, down: d.down})
			}
		}
	}
	m.last = next
	m.primed = true
	return out
}

// addTrafficToAttribution puts this scan's deltas on the attribution entries.
// A live IP's entry carries its delta even when it is zero (counted, nothing
// moved), so the stored total exists as zero rather than missing. An IP without
// a live entry this scan — its connections closed since the previous scan —
// only gets an entry when it moved bytes, stamped with the core's last dispatch
// so presence is never invented for it.
func addTrafficToAttribution(attribution map[string][]model.ClientIpEntry, traffic map[string][]ipTrafficDelta) map[string][]model.ClientIpEntry {
	if len(traffic) == 0 {
		return attribution
	}
	if attribution == nil {
		attribution = make(map[string][]model.ClientIpEntry, len(traffic))
	}
	now := time.Now().Unix()
	for email, deltas := range traffic {
		entries := attribution[email]
		at := make(map[string]int, len(entries))
		for i := range entries {
			at[entries[i].IP] = i
		}
		for _, d := range deltas {
			up, down := d.up, d.down
			if i, ok := at[d.ip]; ok {
				entries[i].Uplink, entries[i].Downlink = &up, &down
				continue
			}
			if up == 0 && down == 0 {
				continue
			}
			ts := d.lastSeen
			if ts <= 0 {
				ts = now
			}
			entries = append(entries, model.ClientIpEntry{IP: d.ip, Timestamp: ts, Uplink: &up, Downlink: &down})
		}
		if len(entries) > 0 {
			attribution[email] = entries
		}
	}
	return attribution
}
