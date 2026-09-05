package group

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	O "github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/urltest"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

type testURLTestOutbound struct {
	O.Adapter
}

func newTestURLTestOutbound(tag string) adapter.Outbound {
	return &testURLTestOutbound{
		Adapter: O.NewAdapter(C.TypeDirect, tag, []string{N.NetworkTCP, N.NetworkUDP, N.NetworkICMP}, nil),
	}
}

func (o *testURLTestOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, nil
}

func (o *testURLTestOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, nil
}

func TestURLTestBalancerValidation(t *testing.T) {
	tags := []string{"a", "b"}
	for _, mode := range []string{
		"",
		URLTestBalanceModeLeastPing,
		URLTestBalanceModeFailover,
		URLTestBalanceModeRoundRobin,
		URLTestBalanceModeRandom,
		URLTestBalanceModeLeastConnection,
		URLTestBalanceModeWeightedRoundRobin,
		URLTestBalanceModeConsistentHash,
	} {
		balancer, err := newURLTestBalancer(mode, tags, nil)
		require.NoError(t, err)
		if mode == "" {
			require.Equal(t, URLTestBalanceModeLeastPing, balancer.mode)
		} else {
			require.Equal(t, mode, balancer.mode)
		}
	}
	_, err := newURLTestBalancer("invalid", tags, nil)
	require.Error(t, err)
	_, err = newURLTestBalancer(URLTestBalanceModeWeightedRoundRobin, tags, []option.URLTestWeight{{Outbound: "missing", Weight: 1}})
	require.Error(t, err)
	_, err = newURLTestBalancer(URLTestBalanceModeWeightedRoundRobin, tags, []option.URLTestWeight{{Outbound: "a"}})
	require.Error(t, err)
	_, err = newURLTestBalancer(URLTestBalanceModeWeightedRoundRobin, tags, []option.URLTestWeight{{Outbound: "a", Weight: 1}, {Outbound: "a", Weight: 2}})
	require.Error(t, err)
}

func TestURLTestBalancerFailoverUsesFirstHealthyOutbound(t *testing.T) {
	outbounds := []adapter.Outbound{newTestURLTestOutbound("a"), newTestURLTestOutbound("b"), newTestURLTestOutbound("c")}
	history := newURLTestHistory(map[string]uint16{"a": 500, "b": 10, "c": 100})
	balancer, err := newURLTestBalancer(URLTestBalanceModeFailover, outboundTags(outbounds), nil)
	require.NoError(t, err)

	require.Equal(t, []string{"a", "a", "a"}, selectOutboundTags(t, balancer, outbounds, history, 3))
	history.DeleteURLTestHistory("a")
	require.Equal(t, []string{"b", "b", "b"}, selectOutboundTags(t, balancer, outbounds, history, 3))
}

func TestURLTestBalancerFailoverReturnsToRecoveredPrimary(t *testing.T) {
	outbounds := []adapter.Outbound{newTestURLTestOutbound("a"), newTestURLTestOutbound("b"), newTestURLTestOutbound("c")}
	history := newURLTestHistory(map[string]uint16{"a": 100, "b": 200, "c": 300})
	balancer, err := newURLTestBalancer(URLTestBalanceModeFailover, outboundTags(outbounds), nil)
	require.NoError(t, err)

	require.False(t, balancer.refresh(N.NetworkTCP, outbounds, history, 0))
	require.Equal(t, "a", balancer.now())

	history.DeleteURLTestHistory("c")
	require.False(t, balancer.refresh(N.NetworkTCP, outbounds, history, 0), "a backup health change must not interrupt connections while the primary remains selected")
	require.Equal(t, "a", balancer.now())

	history.DeleteURLTestHistory("a")
	require.True(t, balancer.refresh(N.NetworkTCP, outbounds, history, 0))
	require.Equal(t, "b", balancer.now())

	history.StoreURLTestHistory("a", &adapter.URLTestHistory{Time: time.Now(), Delay: 500})
	require.True(t, balancer.refresh(N.NetworkTCP, outbounds, history, 0))
	require.Equal(t, "a", balancer.now())
}

func TestURLTestBalancerFailoverFallsOpenInListOrder(t *testing.T) {
	outbounds := []adapter.Outbound{newTestURLTestOutbound("a"), newTestURLTestOutbound("b")}
	history := urltest.NewHistoryStorage()
	balancer, err := newURLTestBalancer(URLTestBalanceModeFailover, outboundTags(outbounds), nil)
	require.NoError(t, err)

	for range 3 {
		selected, available, _ := balancer.selectOutbound(N.NetworkTCP, M.Socksaddr{}, outbounds, history, 0, true)
		require.False(t, available)
		require.Equal(t, "a", selected.Tag())
	}
}

func TestURLTestBalancerFailoverInitialHealthUpdate(t *testing.T) {
	outbounds := []adapter.Outbound{newTestURLTestOutbound("a"), newTestURLTestOutbound("b")}
	history := urltest.NewHistoryStorage()
	balancer, err := newURLTestBalancer(URLTestBalanceModeFailover, outboundTags(outbounds), nil)
	require.NoError(t, err)

	selected, available, _ := balancer.selectOutbound(N.NetworkTCP, M.Socksaddr{}, outbounds, history, 0, true)
	require.False(t, available)
	require.Equal(t, "a", selected.Tag())

	history.StoreURLTestHistory("b", &adapter.URLTestHistory{Time: time.Now(), Delay: 100})
	require.True(t, balancer.refresh(N.NetworkTCP, outbounds, history, 0))
	require.Equal(t, "b", balancer.now())
}

func TestURLTestBalancerLeastPingCompatibility(t *testing.T) {
	outbounds := []adapter.Outbound{newTestURLTestOutbound("a"), newTestURLTestOutbound("b")}
	history := newURLTestHistory(map[string]uint16{"a": 100, "b": 120})
	balancer, err := newURLTestBalancer("", outboundTags(outbounds), nil)
	require.NoError(t, err)

	balancer.selected[N.NetworkTCP] = outbounds[1]
	selected, available, _ := balancer.selectOutbound(N.NetworkTCP, M.Socksaddr{}, outbounds, history, 50, true)
	require.True(t, available)
	require.Equal(t, "b", selected.Tag(), "the current outbound must remain selected while the delay difference is within tolerance")

	history.DeleteURLTestHistory("b")
	selected, available, _ = balancer.selectOutbound(N.NetworkTCP, M.Socksaddr{}, outbounds, history, 50, true)
	require.True(t, available)
	require.Equal(t, "a", selected.Tag())
}

func TestURLTestBalancerRoundRobinUsesHealthyOutbounds(t *testing.T) {
	outbounds := []adapter.Outbound{newTestURLTestOutbound("a"), newTestURLTestOutbound("b"), newTestURLTestOutbound("c")}
	history := newURLTestHistory(map[string]uint16{"a": 100, "c": 300})
	balancer, err := newURLTestBalancer(URLTestBalanceModeRoundRobin, outboundTags(outbounds), nil)
	require.NoError(t, err)

	require.Equal(t, []string{"a", "c", "a", "c"}, selectOutboundTags(t, balancer, outbounds, history, 4))
}

func TestURLTestPreMatchRoundRobinSelectsEachL3Flow(t *testing.T) {
	outbounds := []adapter.Outbound{newTestURLTestOutbound("a"), newTestURLTestOutbound("b")}
	history := newURLTestHistory(map[string]uint16{"a": 100, "b": 200})
	balancer, err := newURLTestBalancer(URLTestBalanceModeRoundRobin, outboundTags(outbounds), nil)
	require.NoError(t, err)
	urlTest := &URLTest{group: &URLTestGroup{
		outbounds: outbounds,
		history:   history,
		balancer:  balancer,
	}}
	destination := M.ParseSocksaddr("192.0.2.1:0")

	var selectedTags []string
	for range 4 {
		selected, acquire := urlTest.SelectPreMatchOutbound(N.NetworkICMP, destination)
		require.NotNil(t, selected)
		require.NotNil(t, acquire)
		require.Nil(t, acquire())
		selectedTags = append(selectedTags, selected.Tag())
	}
	require.Equal(t, []string{"a", "b", "a", "b"}, selectedTags)
}

func TestURLTestPreMatchLeastConnectionReturnsFlowRelease(t *testing.T) {
	outbounds := []adapter.Outbound{newTestURLTestOutbound("a"), newTestURLTestOutbound("b")}
	history := newURLTestHistory(map[string]uint16{"a": 100, "b": 200})
	balancer, err := newURLTestBalancer(URLTestBalanceModeLeastConnection, outboundTags(outbounds), nil)
	require.NoError(t, err)
	urlTest := &URLTest{group: &URLTestGroup{
		outbounds: outbounds,
		history:   history,
		balancer:  balancer,
	}}
	destination := M.ParseSocksaddr("192.0.2.1:0")

	selectedA, acquireA := urlTest.SelectPreMatchOutbound(N.NetworkICMP, destination)
	releaseA := acquireA()
	selectedB, acquireB := urlTest.SelectPreMatchOutbound(N.NetworkICMP, destination)
	require.Equal(t, "a", selectedA.Tag())
	require.Equal(t, "b", selectedB.Tag())
	require.NotNil(t, releaseA)
	require.NotNil(t, acquireB)
	releaseB := acquireB()

	releaseA()
	selectedA2, acquireA2 := urlTest.SelectPreMatchOutbound(N.NetworkICMP, destination)
	require.Equal(t, "a", selectedA2.Tag())
	releaseB()
	releaseA2 := acquireA2()
	releaseA2()
}

func TestURLTestBalancerFallsBackBeforeHealthResults(t *testing.T) {
	outbounds := []adapter.Outbound{newTestURLTestOutbound("a"), newTestURLTestOutbound("b")}
	history := urltest.NewHistoryStorage()
	balancer, err := newURLTestBalancer(URLTestBalanceModeRoundRobin, outboundTags(outbounds), nil)
	require.NoError(t, err)

	first, available, _ := balancer.selectOutbound(N.NetworkTCP, M.Socksaddr{}, outbounds, history, 0, true)
	require.False(t, available)
	require.Equal(t, "a", first.Tag())
	second, available, _ := balancer.selectOutbound(N.NetworkTCP, M.Socksaddr{}, outbounds, history, 0, true)
	require.False(t, available)
	require.Equal(t, "b", second.Tag())
}

func TestURLTestBalancerRandomUsesHealthyOutbounds(t *testing.T) {
	outbounds := []adapter.Outbound{newTestURLTestOutbound("a"), newTestURLTestOutbound("b"), newTestURLTestOutbound("c")}
	history := newURLTestHistory(map[string]uint16{"b": 200})
	balancer, err := newURLTestBalancer(URLTestBalanceModeRandom, outboundTags(outbounds), nil)
	require.NoError(t, err)

	require.Equal(t, []string{"b", "b", "b"}, selectOutboundTags(t, balancer, outbounds, history, 3))
}

func TestURLTestBalancerLeastConnection(t *testing.T) {
	outbounds := []adapter.Outbound{newTestURLTestOutbound("a"), newTestURLTestOutbound("b")}
	history := newURLTestHistory(map[string]uint16{"a": 100, "b": 200})
	balancer, err := newURLTestBalancer(URLTestBalanceModeLeastConnection, outboundTags(outbounds), nil)
	require.NoError(t, err)

	selectedA1, _, releaseA1 := balancer.selectOutbound(N.NetworkTCP, M.Socksaddr{}, outbounds, history, 0, true)
	selectedB, _, releaseB := balancer.selectOutbound(N.NetworkTCP, M.Socksaddr{}, outbounds, history, 0, true)
	selectedA2, _, releaseA2 := balancer.selectOutbound(N.NetworkTCP, M.Socksaddr{}, outbounds, history, 0, true)
	require.Equal(t, "a", selectedA1.Tag())
	require.Equal(t, "b", selectedB.Tag())
	require.Equal(t, "a", selectedA2.Tag())

	releaseA1()
	selectedB2, _, releaseB2 := balancer.selectOutbound(N.NetworkTCP, M.Socksaddr{}, outbounds, history, 0, true)
	require.Equal(t, "b", selectedB2.Tag())
	releaseB()
	releaseA2()
	releaseB2()
	balancer.access.Lock()
	require.Zero(t, balancer.activeConnections["a"])
	require.Zero(t, balancer.activeConnections["b"])
	balancer.access.Unlock()
}

func TestURLTestBalancerConcurrentLeastConnection(t *testing.T) {
	outbounds := []adapter.Outbound{newTestURLTestOutbound("a"), newTestURLTestOutbound("b"), newTestURLTestOutbound("c")}
	history := newURLTestHistory(map[string]uint16{"a": 100, "b": 200, "c": 300})
	balancer, err := newURLTestBalancer(URLTestBalanceModeLeastConnection, outboundTags(outbounds), nil)
	require.NoError(t, err)

	var waitGroup sync.WaitGroup
	var failures atomic.Int32
	for range 100 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			selected, available, release := balancer.selectOutbound(N.NetworkTCP, M.Socksaddr{}, outbounds, history, 0, true)
			if !available || selected == nil || release == nil {
				failures.Add(1)
				return
			}
			release()
		}()
	}
	waitGroup.Wait()
	require.Zero(t, failures.Load())
	balancer.access.Lock()
	require.Zero(t, balancer.activeConnections["a"])
	require.Zero(t, balancer.activeConnections["b"])
	require.Zero(t, balancer.activeConnections["c"])
	balancer.access.Unlock()
}

func TestURLTestBalancerWeightedRoundRobin(t *testing.T) {
	outbounds := []adapter.Outbound{newTestURLTestOutbound("a"), newTestURLTestOutbound("b"), newTestURLTestOutbound("c")}
	history := newURLTestHistory(map[string]uint16{"a": 100, "b": 200, "c": 300})
	balancer, err := newURLTestBalancer(URLTestBalanceModeWeightedRoundRobin, outboundTags(outbounds), []option.URLTestWeight{
		{Outbound: "a", Weight: 1},
		{Outbound: "b", Weight: 2},
		{Outbound: "c", Weight: 3},
	})
	require.NoError(t, err)

	counts := make(map[string]int)
	for _, tag := range selectOutboundTags(t, balancer, outbounds, history, 12) {
		counts[tag]++
	}
	require.Equal(t, map[string]int{"a": 2, "b": 4, "c": 6}, counts)
}

func TestURLTestBalancerConsistentHash(t *testing.T) {
	outbounds := []adapter.Outbound{newTestURLTestOutbound("a"), newTestURLTestOutbound("b"), newTestURLTestOutbound("c")}
	history := newURLTestHistory(map[string]uint16{"a": 100, "b": 200, "c": 300})
	balancer, err := newURLTestBalancer(URLTestBalanceModeConsistentHash, outboundTags(outbounds), nil)
	require.NoError(t, err)
	destination := M.ParseSocksaddr("example.com:443")

	first, _, _ := balancer.selectOutbound(N.NetworkTCP, destination, outbounds, history, 0, true)
	for range 10 {
		selected, _, _ := balancer.selectOutbound(N.NetworkTCP, destination, outbounds, history, 0, true)
		require.Equal(t, first.Tag(), selected.Tag())
	}
}

func TestURLTestBalancerAvailabilityRefresh(t *testing.T) {
	outbounds := []adapter.Outbound{newTestURLTestOutbound("a"), newTestURLTestOutbound("b")}
	history := newURLTestHistory(map[string]uint16{"a": 100, "b": 200})
	balancer, err := newURLTestBalancer(URLTestBalanceModeRoundRobin, outboundTags(outbounds), nil)
	require.NoError(t, err)

	require.False(t, balancer.refresh(N.NetworkTCP, outbounds, history, 0))
	history.DeleteURLTestHistory("b")
	require.True(t, balancer.refresh(N.NetworkTCP, outbounds, history, 0))
	require.False(t, balancer.refresh(N.NetworkTCP, outbounds, history, 0))
}

func TestURLTestTrackedConnectionReleasesOnce(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	var releases atomic.Int32
	tracked := trackURLTestConnection(client, func() {
		releases.Add(1)
	})

	require.NoError(t, tracked.Close())
	_ = tracked.Close()
	require.EqualValues(t, 1, releases.Load())
}

func newURLTestHistory(delays map[string]uint16) *urltest.HistoryStorage {
	history := urltest.NewHistoryStorage()
	for tag, delay := range delays {
		history.StoreURLTestHistory(tag, &adapter.URLTestHistory{Time: time.Now(), Delay: delay})
	}
	return history
}

func outboundTags(outbounds []adapter.Outbound) []string {
	tags := make([]string, 0, len(outbounds))
	for _, outbound := range outbounds {
		tags = append(tags, outbound.Tag())
	}
	return tags
}

func selectOutboundTags(t *testing.T, balancer *urlTestBalancer, outbounds []adapter.Outbound, history *urltest.HistoryStorage, count int) []string {
	t.Helper()
	tags := make([]string, 0, count)
	for range count {
		selected, available, _ := balancer.selectOutbound(N.NetworkTCP, M.Socksaddr{}, outbounds, history, 0, true)
		require.True(t, available)
		require.NotNil(t, selected)
		tags = append(tags, selected.Tag())
	}
	return tags
}

func TestURLTestConsistentHashDistributionAndRemoval(t *testing.T) {
	var candidates []adapter.Outbound
	for i := 1; i <= 8; i++ {
		candidates = append(candidates, newTestURLTestOutbound(fmt.Sprintf("node%d", i)))
	}
	counts := make(map[string]int)
	for i := range 10000 {
		destination := M.ParseSocksaddr(fmt.Sprintf("example%d.com:443", i))
		selected := chooseConsistentHash(N.NetworkTCP, destination, candidates)
		counts[selected.Tag()]++
		if selected != candidates[0] {
			require.Equal(t, selected, chooseConsistentHash(N.NetworkTCP, destination, candidates[1:]), "removing another node must preserve the mapping")
		}
	}
	for _, candidate := range candidates {
		require.InDelta(t, 1250, counts[candidate.Tag()], 250, "uneven distribution for %s", candidate.Tag())
	}
}

func TestURLTestPreMatchDoesNotCountDiscardedFlows(t *testing.T) {
	outbounds := []adapter.Outbound{newTestURLTestOutbound("a"), newTestURLTestOutbound("b")}
	history := newURLTestHistory(map[string]uint16{"a": 100, "b": 200})
	balancer, err := newURLTestBalancer(URLTestBalanceModeLeastConnection, outboundTags(outbounds), nil)
	require.NoError(t, err)
	urlTest := &URLTest{group: &URLTestGroup{outbounds: outbounds, history: history, balancer: balancer}}
	for range 10 {
		_, acquire := urlTest.SelectPreMatchOutbound(N.NetworkUDP, M.Socksaddr{})
		require.NotNil(t, acquire)
	}
	require.Empty(t, balancer.activeConnections)
	selected, acquire := urlTest.SelectPreMatchOutbound(N.NetworkUDP, M.Socksaddr{})
	release := acquire()
	require.EqualValues(t, 1, balancer.activeConnections[selected.Tag()])
	release()
	release()
	require.Zero(t, balancer.activeConnections[selected.Tag()])
}

func TestURLTestPreMatchFallbackDoesNotAdvanceBalancer(t *testing.T) {
	for _, mode := range []string{URLTestBalanceModeRoundRobin, URLTestBalanceModeWeightedRoundRobin, URLTestBalanceModeLeastConnection} {
		t.Run(mode, func(t *testing.T) {
			outbounds := []adapter.Outbound{newTestURLTestOutbound("a"), newTestURLTestOutbound("b")}
			history := newURLTestHistory(map[string]uint16{"a": 100, "b": 200})
			balancer, err := newURLTestBalancer(mode, outboundTags(outbounds), nil)
			require.NoError(t, err)
			urlTest := &URLTest{group: &URLTestGroup{outbounds: outbounds, history: history, balancer: balancer}}
			var tags []string
			for range 4 {
				// Ordinary proxies cannot accept L3 flows; their pre-match choice is discarded.
				_, _ = urlTest.SelectPreMatchOutbound(N.NetworkTCP, M.Socksaddr{})
				selected, _, release := balancer.selectOutbound(N.NetworkTCP, M.Socksaddr{}, outbounds, history, 0, true)
				tags = append(tags, selected.Tag())
				if release != nil {
					release()
				}
			}
			require.Equal(t, []string{"a", "b", "a", "b"}, tags)
		})
	}
}

func TestURLTestRefreshPreservesConnectionSwitchNotification(t *testing.T) {
	for _, mode := range []string{URLTestBalanceModeLeastPing, URLTestBalanceModeFailover} {
		t.Run(mode, func(t *testing.T) {
			outbounds := []adapter.Outbound{newTestURLTestOutbound("a"), newTestURLTestOutbound("b")}
			history := newURLTestHistory(map[string]uint16{"a": 100, "b": 200})
			balancer, err := newURLTestBalancer(mode, outboundTags(outbounds), nil)
			require.NoError(t, err)
			require.False(t, balancer.refresh(N.NetworkTCP, outbounds, history, 0))
			history.DeleteURLTestHistory("a")
			// A new connection can observe the health update before the batch finishes.
			selected, _, _ := balancer.selectOutbound(N.NetworkTCP, M.Socksaddr{}, outbounds, history, 0, true)
			require.Equal(t, "b", selected.Tag())
			require.True(t, balancer.refresh(N.NetworkTCP, outbounds, history, 0))
			require.False(t, balancer.refresh(N.NetworkTCP, outbounds, history, 0))
		})
	}
}
