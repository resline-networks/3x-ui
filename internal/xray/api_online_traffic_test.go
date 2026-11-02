package xray

import (
	"context"
	"net"
	"sync"
	"testing"

	statsService "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
)

// fakeUsersStatsServer answers GetUsersStats with a fixed response and keeps
// the last request so a test can see which flags the panel sent.
type fakeUsersStatsServer struct {
	statsService.UnimplementedStatsServiceServer
	resp *statsService.GetUsersStatsResponse

	mu   sync.Mutex
	last *statsService.GetUsersStatsRequest
}

func (f *fakeUsersStatsServer) GetUsersStats(_ context.Context, req *statsService.GetUsersStatsRequest) (*statsService.GetUsersStatsResponse, error) {
	f.mu.Lock()
	f.last = req
	f.mu.Unlock()
	return f.resp, nil
}

func (f *fakeUsersStatsServer) lastRequest() *statsService.GetUsersStatsRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

func startFakeUsersStats(t *testing.T, resp *statsService.GetUsersStatsResponse) (*XrayAPI, *fakeUsersStatsServer) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeUsersStatsServer{resp: resp}
	srv := grpc.NewServer()
	statsService.RegisterStatsServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	api := &XrayAPI{}
	if err := api.Init(lis.Addr().(*net.TCPAddr).Port); err != nil {
		t.Fatalf("api init: %v", err)
	}
	t.Cleanup(api.Close)
	return api, fake
}

func i64(v int64) *int64 { return &v }

// The resline xray reports, per (user, source IP), the bytes moved since the
// entry was created, and keeps recently closed IPs in offline_ips. An upstream
// core sends neither field.
func ipTrafficResponse() *statsService.GetUsersStatsResponse {
	return &statsService.GetUsersStatsResponse{Users: []*statsService.UserStat{
		{
			Email: "alice",
			Ips: []*statsService.OnlineIPEntry{
				{Ip: "203.0.113.1", LastSeen: 100, Uplink: i64(1000), Downlink: i64(5000)},
				{Ip: "203.0.113.2", LastSeen: 110, Uplink: i64(0), Downlink: i64(0)},
				{Ip: "203.0.113.3", LastSeen: 120}, // core did not count this one
			},
			OfflineIps: []*statsService.OnlineIPEntry{
				{Ip: "203.0.113.9", LastSeen: 90, Uplink: i64(70), Downlink: i64(700)},
			},
		},
		{
			// Every connection closed since the previous poll: not online.
			Email: "bob",
			OfflineIps: []*statsService.OnlineIPEntry{
				{Ip: "198.51.100.4", LastSeen: 80, Uplink: i64(11), Downlink: i64(22)},
			},
		},
	}}
}

func TestGetUsersIPTrafficAsksForOfflineIPsAndKeepsThemApart(t *testing.T) {
	api, fake := startFakeUsersStats(t, ipTrafficResponse())

	users, err := api.GetUsersIPTraffic()
	if err != nil {
		t.Fatalf("GetUsersIPTraffic: %v", err)
	}
	if req := fake.lastRequest(); req == nil || !req.GetIncludeOfflineIps() {
		t.Fatalf("request must ask for offline IPs, got %+v", req)
	}

	byEmail := map[string]OnlineUser{}
	for _, u := range users {
		byEmail[u.Email] = u
	}
	alice := byEmail["alice"]
	if len(alice.IPs) != 3 || len(alice.OfflineIPs) != 1 {
		t.Fatalf("alice: %d online / %d offline IPs, want 3 / 1: %+v", len(alice.IPs), len(alice.OfflineIPs), alice)
	}
	assertBytes(t, alice.IPs[0], 1000, 5000)
	assertBytes(t, alice.IPs[1], 0, 0) // counted, no traffic yet: zero, not missing
	if alice.IPs[2].Uplink != nil || alice.IPs[2].Downlink != nil {
		t.Fatalf("uncounted IP must carry no bytes, got %+v", alice.IPs[2])
	}
	if off := alice.OfflineIPs[0]; off.IP != "203.0.113.9" || off.LastSeen != 90 {
		t.Fatalf("offline entry = %+v", off)
	}
	assertBytes(t, alice.OfflineIPs[0], 70, 700)

	bob, ok := byEmail["bob"]
	if !ok {
		t.Fatalf("a user with only offline IPs must still be returned for its bytes: %+v", users)
	}
	if len(bob.IPs) != 0 || len(bob.OfflineIPs) != 1 {
		t.Fatalf("bob must have no online IPs and one offline IP, got %+v", bob)
	}
}

// GetOnlineUsers feeds the online list (xray_traffic_job): it must keep asking
// without the offline flag, so an offline-only user never reads as online.
func TestGetOnlineUsersStillOmitsOfflineIPs(t *testing.T) {
	api, fake := startFakeUsersStats(t, &statsService.GetUsersStatsResponse{Users: []*statsService.UserStat{
		{Email: "alice", Ips: []*statsService.OnlineIPEntry{
			{Ip: "203.0.113.1", LastSeen: 100, Uplink: i64(1000), Downlink: i64(5000)},
		}},
	}})

	users, err := api.GetOnlineUsers()
	if err != nil {
		t.Fatalf("GetOnlineUsers: %v", err)
	}
	if req := fake.lastRequest(); req == nil || req.GetIncludeOfflineIps() {
		t.Fatalf("online list must not ask for offline IPs, got %+v", req)
	}
	if len(users) != 1 || len(users[0].IPs) != 1 {
		t.Fatalf("users = %+v", users)
	}
	assertBytes(t, users[0].IPs[0], 1000, 5000)
}

func assertBytes(t *testing.T, ip OnlineIP, up, down int64) {
	t.Helper()
	if ip.Uplink == nil || ip.Downlink == nil {
		t.Fatalf("%s: bytes missing, want up=%d down=%d", ip.IP, up, down)
	}
	if *ip.Uplink != up || *ip.Downlink != down {
		t.Fatalf("%s: up=%d down=%d, want up=%d down=%d", ip.IP, *ip.Uplink, *ip.Downlink, up, down)
	}
}
