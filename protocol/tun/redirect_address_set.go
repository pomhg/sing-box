package tun

import (
	"net/netip"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common"
	"go4.org/netipx"
)

// sing-tun dev evaluates address-set bypass through pre-match instead of
// installing these sets in nftables. Publish both sets as one immutable snapshot.
type autoRedirectAddressSets struct {
	include []*netipx.IPSet
	exclude []*netipx.IPSet
}

func (t *Inbound) refreshAutoRedirectAddressSets() {
	t.autoRedirectAddressSets.Store(&autoRedirectAddressSets{
		include: common.FlatMap(t.routeRuleSet, adapter.RuleSet.ExtractIPSet),
		exclude: common.FlatMap(t.routeExcludeRuleSet, adapter.RuleSet.ExtractIPSet),
	})
}

func (t *Inbound) fetchAutoRedirectAddressSets() (include []netip.Prefix, exclude []netip.Prefix, err error) {
	sets := t.autoRedirectAddressSets.Load()
	if sets == nil {
		return nil, nil, nil
	}
	for _, set := range sets.include {
		include = append(include, set.Prefixes()...)
	}
	for _, set := range sets.exclude {
		exclude = append(exclude, set.Prefixes()...)
	}
	return include, exclude, nil
}

func (t *Inbound) bypassAutoRedirectAddress(destination netip.Addr) bool {
	sets := t.autoRedirectAddressSets.Load()
	if sets == nil {
		return false
	}
	for _, set := range sets.exclude {
		if set.Contains(destination) {
			return true
		}
	}
	if len(sets.include) == 0 {
		return false
	}
	for _, set := range sets.include {
		if set.Contains(destination) {
			return false
		}
	}
	return true
}
