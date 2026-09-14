package advrouting

import (
	"net"
	"testing"
)

func state(cfg Config, healthy ...bool) *PluginState {
	p := &PluginState{config: cfg}
	for i, h := range healthy {
		p.routers = append(p.routers, &Router{
			ip:      net.ParseIP("192.168.3.25" + string(rune('1'+i))),
			healthy: h,
		})
	}
	return p
}

func TestHealthyRouters(t *testing.T) {
	cfg := Config{HealthCheckDestination: "192.168.1.1"}

	for _, tc := range []struct {
		name    string
		healthy []bool
		want    []string
		wantErr bool
	}{
		{
			name:    "all healthy",
			healthy: []bool{true, true, true},
			want:    []string{"192.168.3.251", "192.168.3.252", "192.168.3.253"},
		},
		{
			// the surviving routers prove the probe itself works, so a
			// partial verdict is real information and must be honoured
			name:    "partial failure prunes",
			healthy: []bool{true, false, true},
			want:    []string{"192.168.3.251", "192.168.3.253"},
		},
		{
			name:    "single survivor",
			healthy: []bool{false, false, true},
			want:    []string{"192.168.3.253"},
		},
		{
			// the regression: this used to return an error, and Handler4
			// turned that into a dropped request -- no DHCP for anyone
			name:    "unanimous failure fails open to every router",
			healthy: []bool{false, false, false},
			want:    []string{"192.168.3.251", "192.168.3.252", "192.168.3.253"},
		},
		{
			name:    "no routers configured is still an error",
			healthy: nil,
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := state(cfg, tc.healthy...).healthyRouters()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i, w := range tc.want {
				if !got[i].Equal(net.ParseIP(w)) {
					t.Errorf("index %d: got %v, want %s", i, got[i], w)
				}
			}
		})
	}
}

// A router already associated with a client must survive a unanimous failure,
// so an all-fail sweep does not churn every client onto a new gateway.
func TestUnanimousFailureKeepsExistingAssociation(t *testing.T) {
	p := state(Config{HealthCheckDestination: "192.168.1.1"}, false, false, false)

	routers, err := p.healthyRouters()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(routers, net.ParseIP("192.168.3.252")) {
		t.Error("a client's existing router was not retained through a unanimous failure")
	}
}
