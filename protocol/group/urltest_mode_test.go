package group

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	O "github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/urltest"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

type testURLTestOutbound struct {
	O.Adapter
}

func newTestURLTestOutbound(tag string) adapter.Outbound {
	return &testURLTestOutbound{
		Adapter: O.NewAdapter(C.TypeDirect, tag, []string{N.NetworkTCP, N.NetworkUDP}, nil),
	}
}

func (o *testURLTestOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, nil
}

func (o *testURLTestOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, nil
}

func newTestURLTestGroup(t *testing.T, mode string, tags []string, delays map[string]uint16) (*URLTestGroup, *urltest.HistoryStorage) {
	history := urltest.NewHistoryStorage()
	for tag, delay := range delays {
		history.StoreURLTestHistory(tag, &adapter.URLTestHistory{Time: time.Now(), Delay: delay})
	}
	outbounds := make([]adapter.Outbound, 0, len(tags))
	for _, tag := range tags {
		outbounds = append(outbounds, newTestURLTestOutbound(tag))
	}
	ctx := service.ContextWithPtr(context.Background(), history)
	group, err := NewURLTestGroup(ctx, nil, log.NewNOPFactory().Logger(), outbounds, "", 0, 0, mode, 0, false)
	require.NoError(t, err)
	return group, history
}

func TestURLTestModeValidation(t *testing.T) {
	for _, mode := range []string{"", URLTestModeLeastPing, URLTestModeFailover, URLTestModeConsistentHash} {
		_, err := NewURLTest(context.Background(), nil, nil, "test", option.URLTestOutboundOptions{Outbounds: []string{"a"}, Mode: mode})
		require.NoError(t, err, mode)
	}
	_, err := NewURLTest(context.Background(), nil, nil, "test", option.URLTestOutboundOptions{Outbounds: []string{"a"}, Mode: "round_robin"})
	require.Error(t, err)
}

func TestURLTestLeastPingDefault(t *testing.T) {
	group, _ := newTestURLTestGroup(t, "", []string{"a", "b"}, map[string]uint16{"a": 300, "b": 100})
	require.Equal(t, URLTestModeLeastPing, group.mode)
	group.performUpdateCheck()
	require.Equal(t, "b", group.selectedOutboundTCP.Tag())
}

func TestURLTestFailoverUsesFirstHealthyOutbound(t *testing.T) {
	group, history := newTestURLTestGroup(t, URLTestModeFailover, []string{"a", "b", "c"}, map[string]uint16{"a": 500, "b": 10, "c": 100})
	group.performUpdateCheck()
	require.Equal(t, "a", group.selectedOutboundTCP.Tag())
	require.Equal(t, "a", group.selectedOutboundUDP.Tag())

	history.DeleteURLTestHistory("c")
	group.performUpdateCheck()
	require.Equal(t, "a", group.selectedOutboundTCP.Tag())

	history.DeleteURLTestHistory("a")
	group.performUpdateCheck()
	require.Equal(t, "b", group.selectedOutboundTCP.Tag())

	history.StoreURLTestHistory("a", &adapter.URLTestHistory{Time: time.Now(), Delay: 500})
	group.performUpdateCheck()
	require.Equal(t, "a", group.selectedOutboundTCP.Tag())
}

func TestURLTestFailoverFallsOpenInListOrder(t *testing.T) {
	group, history := newTestURLTestGroup(t, URLTestModeFailover, []string{"a", "b"}, nil)
	selected, available := group.Select(N.NetworkTCP)
	require.False(t, available)
	require.Equal(t, "a", selected.Tag())
	group.performUpdateCheck()
	require.Equal(t, "a", group.selectedOutboundTCP.Tag())

	history.StoreURLTestHistory("b", &adapter.URLTestHistory{Time: time.Now(), Delay: 100})
	group.performUpdateCheck()
	require.Equal(t, "b", group.selectedOutboundTCP.Tag())

	history.DeleteURLTestHistory("b")
	group.performUpdateCheck()
	require.Equal(t, "b", group.selectedOutboundTCP.Tag(), "keep the last selection while nothing is healthy")
}

func TestURLTestConsistentHash(t *testing.T) {
	tags := []string{"a", "b", "c", "d"}
	delays := map[string]uint16{"a": 100, "b": 100, "c": 100, "d": 100}
	group, history := newTestURLTestGroup(t, URLTestModeConsistentHash, tags, delays)

	destination := M.ParseSocksaddrHostPort("example.com", 443)
	selected := group.selectConsistentHash(N.NetworkTCP, destination)
	for range 16 {
		require.Equal(t, selected, group.selectConsistentHash(N.NetworkTCP, destination))
	}

	counts := make(map[string]int)
	assignments := make(map[string]string)
	for i := range 1000 {
		key := M.ParseSocksaddrHostPort(fmt.Sprint("host-", i, ".example"), 443)
		tag := group.selectConsistentHash(N.NetworkTCP, key).Tag()
		counts[tag]++
		assignments[key.String()] = tag
	}
	for _, tag := range tags {
		require.Greater(t, counts[tag], 150, tag)
	}

	history.DeleteURLTestHistory("c")
	for i := range 1000 {
		key := M.ParseSocksaddrHostPort(fmt.Sprint("host-", i, ".example"), 443)
		tag := group.selectConsistentHash(N.NetworkTCP, key).Tag()
		require.NotEqual(t, "c", tag)
		if previous := assignments[key.String()]; previous != "c" {
			require.Equal(t, previous, tag, "only flows of the removed outbound should move")
		}
	}
}

func TestURLTestConsistentHashFallsBackBeforeHealthResults(t *testing.T) {
	group, _ := newTestURLTestGroup(t, URLTestModeConsistentHash, []string{"a", "b"}, nil)
	require.NotNil(t, group.selectConsistentHash(N.NetworkUDP, M.ParseSocksaddrHostPort("1.1.1.1", 53)))
}

func TestURLTestConsistentHashReferencesAllCandidates(t *testing.T) {
	group, history := newTestURLTestGroup(t, URLTestModeConsistentHash, []string{"a", "b", "c"}, map[string]uint16{"a": 100, "b": 100})
	outbound := &URLTest{group: group}
	require.Equal(t, []string{"a", "b"}, outbound.References())
	history.DeleteURLTestHistory("a")
	history.DeleteURLTestHistory("b")
	require.Equal(t, []string{"a", "b", "c"}, outbound.References())
}

func TestURLTestConsistentHashPoolChange(t *testing.T) {
	group, history := newTestURLTestGroup(t, URLTestModeConsistentHash, []string{"a", "b"}, map[string]uint16{"a": 100, "b": 100})
	group.performUpdateCheck()
	require.Equal(t, []string{"a", "b"}, group.candidateTags[N.NetworkTCP])
	history.DeleteURLTestHistory("b")
	group.performUpdateCheck()
	require.Equal(t, []string{"a"}, group.candidateTags[N.NetworkTCP])
}

func TestURLTestPreMatchSelection(t *testing.T) {
	group, _ := newTestURLTestGroup(t, URLTestModeConsistentHash, []string{"a", "b", "c"}, map[string]uint16{"a": 100, "b": 100, "c": 100})
	outbound := &URLTest{group: group}
	destination := M.ParseSocksaddrHostPort("10.0.0.1", 443)
	require.Equal(t, group.selectConsistentHash(N.NetworkTCP, destination), outbound.SelectPreMatchOutbound(N.NetworkTCP, destination))

	group, _ = newTestURLTestGroup(t, URLTestModeFailover, []string{"a", "b"}, map[string]uint16{"b": 100})
	group.performUpdateCheck()
	outbound = &URLTest{group: group}
	require.Equal(t, "b", outbound.SelectPreMatchOutbound(N.NetworkTCP, destination).Tag())
}
