package group

import (
	"crypto/sha256"
	"encoding/binary"
	"math/rand/v2"
	"net"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

const (
	URLTestBalanceModeLeastPing          = "least_ping"
	URLTestBalanceModeFailover           = "failover"
	URLTestBalanceModeRoundRobin         = "round_robin"
	URLTestBalanceModeRandom             = "random"
	URLTestBalanceModeLeastConnection    = "least_connection"
	URLTestBalanceModeWeightedRoundRobin = "weighted_round_robin"
	URLTestBalanceModeConsistentHash     = "consistent_hash"
)

type urlTestHistoryReader interface {
	LoadURLTestHistory(tag string) *adapter.URLTestHistory
}

type urlTestBalancer struct {
	outboundManager         adapter.OutboundManager
	mode                    string
	weights                 map[string]uint16
	access                  sync.Mutex
	roundRobinIndex         map[string]uint64
	weightedCurrent         map[string]map[string]int64
	activeConnections       map[string]int64
	selected                map[string]adapter.Outbound
	interruptPending        map[string]bool
	available               map[string][]string
	availabilityInitialized map[string]bool
}

func newURLTestBalancer(mode string, outbounds []string, weights []option.URLTestWeight) (*urlTestBalancer, error) {
	if mode == "" {
		mode = URLTestBalanceModeLeastPing
	}
	switch mode {
	case URLTestBalanceModeLeastPing,
		URLTestBalanceModeFailover,
		URLTestBalanceModeRoundRobin,
		URLTestBalanceModeRandom,
		URLTestBalanceModeLeastConnection,
		URLTestBalanceModeWeightedRoundRobin,
		URLTestBalanceModeConsistentHash:
	default:
		return nil, E.New("unsupported URLTest balance mode: ", mode)
	}
	outboundSet := make(map[string]bool, len(outbounds))
	for _, outbound := range outbounds {
		outboundSet[outbound] = true
	}
	weightMap := make(map[string]uint16, len(weights))
	for _, item := range weights {
		if !outboundSet[item.Outbound] {
			return nil, E.New("weight outbound not found: ", item.Outbound)
		}
		if item.Weight == 0 {
			return nil, E.New("weight must be greater than zero for outbound: ", item.Outbound)
		}
		if _, loaded := weightMap[item.Outbound]; loaded {
			return nil, E.New("duplicate weight for outbound: ", item.Outbound)
		}
		weightMap[item.Outbound] = item.Weight
	}
	return &urlTestBalancer{
		mode:                    mode,
		weights:                 weightMap,
		roundRobinIndex:         make(map[string]uint64),
		weightedCurrent:         make(map[string]map[string]int64),
		activeConnections:       make(map[string]int64),
		selected:                make(map[string]adapter.Outbound),
		interruptPending:        make(map[string]bool),
		available:               make(map[string][]string),
		availabilityInitialized: make(map[string]bool),
	}, nil
}

func (b *urlTestBalancer) now() string {
	b.access.Lock()
	defer b.access.Unlock()
	if selected := b.selected[N.NetworkTCP]; selected != nil {
		return selected.Tag()
	}
	if selected := b.selected[N.NetworkUDP]; selected != nil {
		return selected.Tag()
	}
	return ""
}

func (b *urlTestBalancer) realTag(detour adapter.Outbound) string {
	if b.outboundManager == nil {
		return detour.Tag()
	}
	return RealTag(b.outboundManager, detour)
}

func (b *urlTestBalancer) selectOutbound(network string, destination M.Socksaddr, outbounds []adapter.Outbound, history urlTestHistoryReader, tolerance uint16, acquire bool) (adapter.Outbound, bool, func()) {
	b.access.Lock()
	defer b.access.Unlock()
	selected, available := b.chooseOutboundLocked(network, destination, outbounds, history, tolerance, acquire, acquire)
	if selected == nil {
		return nil, false, nil
	}
	if !acquire {
		return selected, available, nil
	}
	b.recordSelectionLocked(network, selected)
	if b.mode != URLTestBalanceModeLeastConnection {
		return selected, available, nil
	}
	return selected, available, b.acquireLocked(selected.Tag())
}

// Select a flow without reserving a connection: pre-match callers can discard
// the result, and the dispatcher may reject it before creating a tracker.
func (b *urlTestBalancer) selectFlowOutbound(network string, destination M.Socksaddr, outbounds []adapter.Outbound, history urlTestHistoryReader, tolerance uint16) (adapter.Outbound, func() func()) {
	b.access.Lock()
	defer b.access.Unlock()
	selected, _ := b.chooseOutboundLocked(network, destination, outbounds, history, tolerance, true, false)
	if selected == nil {
		return nil, nil
	}
	candidates, _ := b.availableURLTestOutbounds(network, outbounds, history)
	return selected, func() func() {
		b.access.Lock()
		defer b.access.Unlock()
		b.recordSelectionLocked(network, selected)
		switch b.mode {
		case URLTestBalanceModeRoundRobin, URLTestBalanceModeLeastConnection:
			b.roundRobinIndex[network]++
		case URLTestBalanceModeWeightedRoundRobin:
			b.commitWeightedRoundRobinLocked(network, candidates, selected)
		}
		if b.mode == URLTestBalanceModeLeastConnection {
			return b.acquireLocked(selected.Tag())
		}
		return nil
	}
}

// Remember switches observed by new connections until the health batch has
// finished and refresh can notify the interrupt group.
func (b *urlTestBalancer) recordSelectionLocked(network string, selected adapter.Outbound) {
	if b.mode == URLTestBalanceModeLeastPing || b.mode == URLTestBalanceModeFailover {
		if previous := b.selected[network]; previous != nil && previous != selected {
			b.interruptPending[network] = true
		}
	}
	b.selected[network] = selected
}

func (b *urlTestBalancer) acquireLocked(tag string) func() {
	b.activeConnections[tag]++
	return sync.OnceFunc(func() { b.release(tag) })
}

func (b *urlTestBalancer) chooseOutboundLocked(network string, destination M.Socksaddr, outbounds []adapter.Outbound, history urlTestHistoryReader, tolerance uint16, advance bool, commit bool) (adapter.Outbound, bool) {
	if b.mode == URLTestBalanceModeLeastPing {
		return b.chooseLeastPingLocked(network, outbounds, history, tolerance)
	}
	candidates, available := b.availableURLTestOutbounds(network, outbounds, history)
	if len(candidates) == 0 {
		return nil, false
	}
	if b.mode == URLTestBalanceModeFailover {
		return candidates[0], available
	}
	if !advance {
		if selected := b.selected[network]; containsOutbound(candidates, selected) {
			return selected, available
		}
		return candidates[0], available
	}
	switch b.mode {
	case URLTestBalanceModeRoundRobin:
		index := b.roundRobinIndex[network]
		if commit {
			b.roundRobinIndex[network] = index + 1
		}
		return candidates[index%uint64(len(candidates))], available
	case URLTestBalanceModeRandom:
		return candidates[rand.IntN(len(candidates))], available
	case URLTestBalanceModeLeastConnection:
		minConnections := b.activeConnections[candidates[0].Tag()]
		leastLoaded := []adapter.Outbound{candidates[0]}
		for _, candidate := range candidates[1:] {
			active := b.activeConnections[candidate.Tag()]
			if active < minConnections {
				minConnections = active
				leastLoaded = []adapter.Outbound{candidate}
			} else if active == minConnections {
				leastLoaded = append(leastLoaded, candidate)
			}
		}
		index := b.roundRobinIndex[network]
		if commit {
			b.roundRobinIndex[network] = index + 1
		}
		return leastLoaded[index%uint64(len(leastLoaded))], available
	case URLTestBalanceModeWeightedRoundRobin:
		return b.chooseWeightedRoundRobinLocked(network, candidates, commit), available
	case URLTestBalanceModeConsistentHash:
		return chooseConsistentHash(network, destination, candidates), available
	default:
		panic("invalid URLTest balance mode: " + b.mode)
	}
}

func (b *urlTestBalancer) chooseLeastPingLocked(network string, outbounds []adapter.Outbound, history urlTestHistoryReader, tolerance uint16) (adapter.Outbound, bool) {
	var minDelay uint16
	var minOutbound adapter.Outbound
	if selected := b.selected[network]; selected != nil && common.Contains(selected.Network(), network) {
		if selectedHistory := history.LoadURLTestHistory(b.realTag(selected)); selectedHistory != nil {
			minOutbound = selected
			minDelay = selectedHistory.Delay
		}
	}
	for _, detour := range outbounds {
		if !common.Contains(detour.Network(), network) {
			continue
		}
		detourHistory := history.LoadURLTestHistory(b.realTag(detour))
		if detourHistory == nil {
			continue
		}
		if minOutbound == nil || uint32(minDelay) > uint32(detourHistory.Delay)+uint32(tolerance) {
			minDelay = detourHistory.Delay
			minOutbound = detour
		}
	}
	if minOutbound != nil {
		return minOutbound, true
	}
	for _, detour := range outbounds {
		if common.Contains(detour.Network(), network) {
			return detour, false
		}
	}
	return nil, false
}

func (b *urlTestBalancer) chooseWeightedRoundRobinLocked(network string, candidates []adapter.Outbound, commit bool) adapter.Outbound {
	current := b.weightedCurrent[network]
	var selected adapter.Outbound
	var selectedCurrent int64
	for _, candidate := range candidates {
		score := current[candidate.Tag()] + int64(b.weight(candidate.Tag()))
		if selected == nil || score > selectedCurrent {
			selected = candidate
			selectedCurrent = score
		}
	}
	if commit {
		b.commitWeightedRoundRobinLocked(network, candidates, selected)
	}
	return selected
}

func (b *urlTestBalancer) commitWeightedRoundRobinLocked(network string, candidates []adapter.Outbound, selected adapter.Outbound) {
	current := b.weightedCurrent[network]
	if current == nil {
		current = make(map[string]int64)
		b.weightedCurrent[network] = current
	}
	candidateTags := make(map[string]bool, len(candidates))
	var totalWeight int64
	for _, candidate := range candidates {
		tag := candidate.Tag()
		candidateTags[tag] = true
		weight := int64(b.weight(tag))
		totalWeight += weight
		current[tag] += weight
	}
	for tag := range current {
		if !candidateTags[tag] {
			delete(current, tag)
		}
	}
	current[selected.Tag()] -= totalWeight
}

func (b *urlTestBalancer) weight(tag string) uint16 {
	if weight := b.weights[tag]; weight > 0 {
		return weight
	}
	return 1
}

func (b *urlTestBalancer) release(tag string) {
	b.access.Lock()
	defer b.access.Unlock()
	if b.activeConnections[tag] > 0 {
		b.activeConnections[tag]--
	}
}

func (b *urlTestBalancer) invalidate(outbound adapter.Outbound) {
	b.access.Lock()
	defer b.access.Unlock()
	for network, selected := range b.selected {
		if selected == outbound {
			if b.mode == URLTestBalanceModeLeastPing || b.mode == URLTestBalanceModeFailover {
				b.interruptPending[network] = true
			}
			delete(b.selected, network)
		}
	}
}

func (b *urlTestBalancer) refresh(network string, outbounds []adapter.Outbound, history urlTestHistoryReader, tolerance uint16) bool {
	b.access.Lock()
	defer b.access.Unlock()
	pending := b.interruptPending[network]
	delete(b.interruptPending, network)
	if b.mode == URLTestBalanceModeLeastPing {
		selected, available := b.chooseLeastPingLocked(network, outbounds, history, tolerance)
		current := b.selected[network]
		if selected == nil || (current != nil && (!available || selected == current)) {
			return pending
		}
		b.selected[network] = selected
		return pending || current != nil
	}
	candidates, _ := b.availableURLTestOutbounds(network, outbounds, history)
	tags := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		tags = append(tags, candidate.Tag())
	}
	if b.mode == URLTestBalanceModeFailover {
		current := b.selected[network]
		var selected adapter.Outbound
		if len(candidates) > 0 {
			selected = candidates[0]
			b.selected[network] = selected
		} else {
			delete(b.selected, network)
		}
		changed := pending || current != nil && selected != current
		b.available[network] = tags
		b.availabilityInitialized[network] = true
		return changed
	}
	changed := b.availabilityInitialized[network] && !equalStrings(b.available[network], tags)
	b.available[network] = tags
	b.availabilityInitialized[network] = true
	if current := b.selected[network]; !containsOutbound(candidates, current) {
		if len(candidates) > 0 {
			b.selected[network] = candidates[0]
		} else {
			delete(b.selected, network)
		}
	}
	return changed
}

func (b *urlTestBalancer) availableURLTestOutbounds(network string, outbounds []adapter.Outbound, history urlTestHistoryReader) ([]adapter.Outbound, bool) {
	healthy := make([]adapter.Outbound, 0, len(outbounds))
	fallback := make([]adapter.Outbound, 0, len(outbounds))
	for _, detour := range outbounds {
		if !common.Contains(detour.Network(), network) {
			continue
		}
		fallback = append(fallback, detour)
		if history.LoadURLTestHistory(b.realTag(detour)) != nil {
			healthy = append(healthy, detour)
		}
	}
	if len(healthy) > 0 {
		return healthy, true
	}
	return fallback, false
}

func containsOutbound(outbounds []adapter.Outbound, outbound adapter.Outbound) bool {
	if outbound == nil {
		return false
	}
	for _, candidate := range outbounds {
		if candidate == outbound {
			return true
		}
	}
	return false
}

func equalStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func chooseConsistentHash(network string, destination M.Socksaddr, candidates []adapter.Outbound) adapter.Outbound {
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
	// Rendezvous scores need avalanche behavior even for similar node tags.
	sum := sha256.Sum256([]byte(key + "\x00" + tag))
	return binary.BigEndian.Uint64(sum[:8])
}

type urlTestTrackedConn struct {
	N.ExtendedConn
	release func()
	once    sync.Once
}

func trackURLTestConnection(conn net.Conn, release func()) net.Conn {
	if release == nil {
		return conn
	}
	return &urlTestTrackedConn{
		ExtendedConn: bufio.NewExtendedConn(conn),
		release:      release,
	}
}

func (c *urlTestTrackedConn) Close() error {
	c.once.Do(c.release)
	return c.ExtendedConn.Close()
}

func (c *urlTestTrackedConn) Upstream() any {
	return c.ExtendedConn
}

func (c *urlTestTrackedConn) ReaderReplaceable() bool {
	return true
}

func (c *urlTestTrackedConn) WriterReplaceable() bool {
	return true
}

type urlTestTrackedPacketConn struct {
	net.PacketConn
	release func()
	once    sync.Once
}

func trackURLTestPacketConnection(conn net.PacketConn, release func()) net.PacketConn {
	if release == nil {
		return conn
	}
	return &urlTestTrackedPacketConn{
		PacketConn: conn,
		release:    release,
	}
}

func (c *urlTestTrackedPacketConn) Close() error {
	c.once.Do(c.release)
	return c.PacketConn.Close()
}

func (c *urlTestTrackedPacketConn) Upstream() any {
	return bufio.NewPacketConn(c.PacketConn)
}

func (c *urlTestTrackedPacketConn) ReaderReplaceable() bool {
	return true
}

func (c *urlTestTrackedPacketConn) WriterReplaceable() bool {
	return true
}
