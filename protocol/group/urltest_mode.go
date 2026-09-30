package group

import (
	"crypto/sha256"
	"encoding/binary"
	"slices"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
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
		if g.history.LoadURLTestHistory(RealTag(detour, network)) != nil {
			healthy = append(healthy, detour)
		}
	}
	if len(healthy) > 0 {
		return healthy
	}
	return fallback
}

// updateCandidateTags stores the current candidates to state,
// and returns whether they differ from the previously stored ones.
func (g *URLTestGroup) updateCandidateTags(state map[string][]string) bool {
	var changed bool
	for _, network := range []string{N.NetworkTCP, N.NetworkUDP} {
		tags := common.Map(g.candidates(network), adapter.Outbound.Tag)
		previousTags, initialized := state[network]
		if initialized && !slices.Equal(previousTags, tags) {
			changed = true
		}
		state[network] = tags
	}
	return changed
}

// interruptIfCandidatesChanged must only be called after a URL test: update
// checks also run on every dial failure, which would interrupt connections of
// unrelated outbounds.
func (g *URLTestGroup) interruptIfCandidatesChanged() {
	if g.updateCandidateTags(g.testedCandidateTags) {
		g.interruptGroup.Interrupt(g.interruptExternalConnections)
	}
}

func (g *URLTestGroup) selectFirstAvailable(network string) (adapter.Outbound, bool) {
	var fallback adapter.Outbound
	for _, detour := range g.outbounds {
		if !common.Contains(detour.Network(), network) {
			continue
		}
		if g.history.LoadURLTestHistory(RealTag(detour, network)) != nil {
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
