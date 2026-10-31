package xray

import (
	"context"
	"net"
	"testing"

	statscmd "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
)

type fakeStats struct {
	statscmd.UnimplementedStatsServiceServer
	lastReset bool
}

func (f *fakeStats) QueryStats(_ context.Context, r *statscmd.QueryStatsRequest) (*statscmd.QueryStatsResponse, error) {
	f.lastReset = r.Reset_
	return &statscmd.QueryStatsResponse{Stat: []*statscmd.Stat{
		{Name: "user>>>u1>>>traffic>>>uplink", Value: 10},
		{Name: "user>>>u1>>>traffic>>>downlink", Value: 200},
		{Name: "user>>>u2>>>traffic>>>downlink", Value: 5},
		{Name: "inbound>>>vless>>>traffic>>>uplink", Value: 999},
		{Name: "user>>>u3>>>online", Value: 1},
	}}, nil
}

func (f *fakeStats) GetSysStats(context.Context, *statscmd.SysStatsRequest) (*statscmd.SysStatsResponse, error) {
	return &statscmd.SysStatsResponse{Uptime: 42, NumGoroutine: 7, Alloc: 1024}, nil
}

func TestAPIAgainstFakeXray(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	fake := &fakeStats{}
	statscmd.RegisterStatsServiceServer(srv, fake)
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)

	api, err := Dial(lis.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	ctx := context.Background()

	traffic, err := api.UserTraffic(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if !fake.lastReset {
		t.Error("reset не передан в Xray")
	}
	want := map[string]Traffic{"u1": {Uplink: 10, Downlink: 200}, "u2": {Downlink: 5}}
	if len(traffic) != len(want) {
		t.Fatalf("трафик: %+v", traffic)
	}
	for k, v := range want {
		if traffic[k] != v {
			t.Errorf("%s: %+v, ожидалось %+v", k, traffic[k], v)
		}
	}

	sys, err := api.SysStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sys.Uptime.Seconds() != 42 || sys.Goroutines != 7 || sys.MemAlloc != 1024 {
		t.Errorf("sys: %+v", sys)
	}
}

func TestParseUserStat(t *testing.T) {
	for name, ok := range map[string]bool{
		"user>>>a@b>>>traffic>>>uplink":   true,
		"user>>>a@b>>>traffic>>>downlink": true,
		"user>>>>>>traffic>>>uplink":      false,
		"user>>>a>>>traffic>>>sideways":   false,
		"user>>>a>>>online":               false,
		"inbound>>>a>>>traffic>>>uplink":  false,
	} {
		if _, _, got := parseUserStat(name); got != ok {
			t.Errorf("%s: %v", name, got)
		}
	}
}
