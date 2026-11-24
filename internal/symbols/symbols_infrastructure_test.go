package symbols_test

import (
	"testing"

	"github.com/dgethings/chunt/internal/symbols"
)

// Infrastructure object extraction (chunt-otg.7): vrf, track, key chain,
// dhcp pool, sla (+ legacy rtr), monitor session, and time-range parse as
// flat lines in grammar v0.3.1; extraction is hostname-style with numeric
// guards on the number-keyed kinds.

func TestExtractInfrastructureKinds(t *testing.T) {
	src := `vrf definition BLUE
 address-family ipv4
!
ip vrf LEGACY
 rd 100:1
!
track 1 interface GigabitEthernet0/0 line-protocol
!
key chain KC1
 key 1
  key-string cisco
!
ip dhcp pool POOL1
 network 10.0.0.0 255.255.255.0
!
ip sla 1
 icmp-echo 8.8.8.8
!
rtr 2
 type echo protocol ipIcmpEcho 9.9.9.9
!
monitor session 1 source interface Gi0/0
monitor session 1 destination interface Gi0/1
!
time-range TR1
 periodic weekdays 08:00 to 17:00
!
`
	root, content := parseRoot(t, src)
	syms := symbols.Extract("file:///t.ios.j2", root, content)
	type want struct {
		kind symbols.Kind
		name string
	}
	wants := []want{
		{symbols.KindVRF, "BLUE"},
		{symbols.KindVRF, "LEGACY"}, // legacy ip vrf folds into KindVRF
		{symbols.KindTrack, "1"},
		{symbols.KindKeyChain, "KC1"},
		{symbols.KindDHCPPool, "POOL1"},
		{symbols.KindSLA, "1"},
		{symbols.KindSLA, "2"}, // legacy rtr folds into KindSLA
		{symbols.KindMonitorSession, "1"},
		{symbols.KindMonitorSession, "1"}, // source + destination lines
		{symbols.KindTimeRange, "TR1"},
	}
	if len(syms) != len(wants) {
		t.Fatalf("got %d symbols, want %d: %+v", len(syms), len(wants), syms)
	}
	for i, w := range wants {
		if syms[i].Kind != w.kind || syms[i].Name != w.name {
			t.Errorf("symbol %d: got (%s, %q), want (%s, %q)", i, syms[i].Kind, syms[i].Name, w.kind, w.name)
		}
	}
}

// Other-arg forms of the number-keyed commands must not register symbols:
// `track timer`, `ip sla schedule`, `monitor session` with a non-numeric
// argument, and the interface-level `ip vrf forwarding`.
func TestInfrastructureNonDefinitionFormsSkipped(t *testing.T) {
	src := `interface Gi0/0
 ip vrf forwarding MGMT
!
track timer interface
ip sla schedule 1 life forever start-time now
ip dhcp excluded-address 10.0.0.1 10.0.0.9
ip dhcp snooping vlan 10
key 1
!
`
	root, content := parseRoot(t, src)
	syms := symbols.Extract("file:///t.ios.j2", root, content)
	for _, s := range syms {
		switch s.Kind {
		case symbols.KindVRF, symbols.KindTrack, symbols.KindSLA, symbols.KindDHCPPool, symbols.KindKeyChain:
			t.Errorf("non-definition form registered as %s %q", s.Kind, s.Name)
		}
	}
}
