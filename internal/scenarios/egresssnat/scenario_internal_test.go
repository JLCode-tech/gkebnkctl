package egresssnat

import (
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/scenarios"
)

// TestTunnelNetworkFollowsPattern pins the default tunnel network to the
// cluster's interface pattern, matching the Infra egressDefaults phase 23b
// renders: single-interface clusters only have the external Infra VLAN,
// dual-interface uses the internal one, and an explicit option always wins.
func TestTunnelNetworkFollowsPattern(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		opt     string
		want    string
	}{
		{"external-only", intent.PatternExternalOnly, "", "external"},
		{"dual-interface", intent.PatternDualInterface, "", "internal"},
		{"host-device alias", intent.PatternHostDevice, "", "external"},
		{"option wins", intent.PatternExternalOnly, "custom-vlan", "custom-vlan"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sctx := &scenarios.Context{
				Cluster: &intent.Cluster{Pattern: tc.pattern},
				Options: map[string]string{},
			}
			if tc.opt != "" {
				sctx.Options["tunnel-network"] = tc.opt
			}
			if got := tunnelNetwork(sctx); got != tc.want {
				t.Errorf("tunnelNetwork(%s) = %q, want %q", tc.pattern, got, tc.want)
			}
		})
	}
	if got := tunnelNetwork(&scenarios.Context{Options: map[string]string{}}); got != "external" {
		t.Errorf("nil cluster fallback = %q, want external", got)
	}
}
