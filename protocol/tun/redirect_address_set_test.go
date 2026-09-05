package tun

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing-tun/gtcpip/header"
	"github.com/stretchr/testify/require"
	"go4.org/netipx"
)

type addressSetRule struct {
	adapter.RuleSet
	sets []*netipx.IPSet
}

func (r *addressSetRule) ExtractIPSet() []*netipx.IPSet { return r.sets }

type addressSetRouter struct {
	adapter.Router
	calls int
}

func (r *addressSetRouter) PreMatch(adapter.InboundContext, []byte) adapter.PreMatchResult {
	r.calls++
	return adapter.PreMatchResult{Action: adapter.PreMatchContinue}
}

func testAddressSet(t *testing.T, prefixes ...string) *netipx.IPSet {
	t.Helper()
	var builder netipx.IPSetBuilder
	for _, prefix := range prefixes {
		builder.AddPrefix(netip.MustParsePrefix(prefix))
	}
	set, err := builder.IPSet()
	require.NoError(t, err)
	return set
}

func TestAutoRedirectAddressSetPreMatch(t *testing.T) {
	router := &addressSetRouter{}
	inbound := &Inbound{router: router}
	source := netip.MustParseAddrPort("10.0.0.1:40000")
	destination := func(address string) netip.AddrPort { return netip.AddrPortFrom(netip.MustParseAddr(address), 443) }
	judge := func(address string) tun.FlowAction {
		return (*autoRedirectHandler)(inbound).JudgeFlow(uint8(header.TCPProtocolNumber), source, destination(address), nil).Action
	}
	require.Equal(t, tun.ActionAccept, judge("192.0.2.1"), "without sets, use normal pre-match")
	inbound.routeRuleSet = []adapter.RuleSet{&addressSetRule{sets: []*netipx.IPSet{testAddressSet(t, "192.0.2.0/24", "2001:db8::/32")}}}
	inbound.routeExcludeRuleSet = []adapter.RuleSet{&addressSetRule{sets: []*netipx.IPSet{testAddressSet(t, "192.0.2.128/25", "2001:db8:1::/48")}}}
	inbound.refreshAutoRedirectAddressSets()
	for _, address := range []string{"192.0.2.1", "2001:db8:2::1"} {
		require.Equal(t, tun.ActionAccept, judge(address))
	}
	calls := router.calls
	for _, address := range []string{"192.0.2.129", "198.51.100.1", "2001:db8:1::1", "2001:db9::1"} {
		require.Equal(t, tun.ActionBypass, judge(address), address)
	}
	require.Equal(t, calls, router.calls, "bypassed addresses must not enter routing")
	include, exclude, err := inbound.fetchAutoRedirectAddressSets()
	require.NoError(t, err)
	require.ElementsMatch(t, []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("2001:db8::/32")}, include)
	require.ElementsMatch(t, []netip.Prefix{netip.MustParsePrefix("192.0.2.128/25"), netip.MustParsePrefix("2001:db8:1::/48")}, exclude)

	// Rule-set updates must discard old exclusions and inclusion restrictions.
	inbound.routeRuleSet = nil
	inbound.routeExcludeRuleSet = nil
	inbound.refreshAutoRedirectAddressSets()
	require.Equal(t, tun.ActionAccept, judge("198.51.100.1"))
	require.Equal(t, tun.ActionAccept, judge("192.0.2.129"))
	include, exclude, err = inbound.fetchAutoRedirectAddressSets()
	require.NoError(t, err)
	require.Empty(t, include)
	require.Empty(t, exclude)
}
