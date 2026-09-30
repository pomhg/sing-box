package group

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common"
	M "github.com/sagernet/sing/common/metadata"
)

const (
	URLTestModeLeastPing      = "least_ping"
	URLTestModeFailover       = "failover"
	URLTestModeConsistentHash = "consistent_hash"
)

// candidates returns outbounds with a valid URL test result in list order,
// or all network-compatible outbounds if none has one.
func (g *URLTestGroup) candidates(network string) []adapter.Outbound {
	var healthy, fallback []adapter.Outbound
	for _, detour := range g.outbounds {
		if !common.Contains(detour.Network(), network) {
			continue
		}
		fallback = append(fallback, detour)
		if g.history.LoadURLTestHistory(RealTag(g.outbound, detour)) != nil {
			healthy = append(healthy, detour)
		}
	}
	if len(healthy) > 0 {
		return healthy
	}
	return fallback
}

func (g *URLTestGroup) selectFirstAvailable(network string) (adapter.Outbound, bool) {
	var fallback adapter.Outbound
	for _, detour := range g.outbounds {
		if !common.Contains(detour.Network(), network) {
			continue
		}
		if g.history.LoadURLTestHistory(RealTag(g.outbound, detour)) != nil {
			return detour, true
		}
		if fallback == nil {
			fallback = detour
		}
	}
	return fallback, false
}

func (g *URLTestGroup) selectConsistentHash(network string, destination M.Socksaddr) adapter.Outbound {
	candidates := g.candidates(network)
	if len(candidates) == 0 {
		return nil
	}
	key := network + "\x00" + destination.String()
	selected := candidates[0]
	selectedScore := rendezvousHash(key, selected.Tag())
	for _, candidate := range candidates[1:] {
		score := rendezvousHash(key, candidate.Tag())
		if score > selectedScore {
			selected = candidate
			selectedScore = score
		}
	}
	return selected
}

func rendezvousHash(key string, tag string) uint64 {
	sum := sha256.Sum256([]byte(key + "\x00" + tag))
	return binary.BigEndian.Uint64(sum[:8])
}
