package route

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	O "github.com/sagernet/sing-box/adapter/outbound"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

type testOutbound struct {
	O.Adapter
}

func newTestOutbound(tag string) *testOutbound {
	return &testOutbound{Adapter: O.NewAdapter(C.TypeDirect, tag, []string{N.NetworkTCP, N.NetworkUDP}, nil)}
}

func (o *testOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, nil
}

func (o *testOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, nil
}

type testGroup struct {
	*testOutbound
	selected adapter.Outbound
}

func (g *testGroup) All() []string                     { return nil }
func (g *testGroup) Selected(string) adapter.Outbound  { return g.selected }
func (g *testGroup) AttachConnection(io.Closer) func() { return func() {} }

type testDestinationGroup struct {
	testGroup
	byDestination map[string]adapter.Outbound
}

func (g *testDestinationGroup) SelectedFor(_ string, destination M.Socksaddr) adapter.Outbound {
	return g.byDestination[destination.String()]
}

func TestResolveOutboundUsesDestination(t *testing.T) {
	a, b := newTestOutbound("a"), newTestOutbound("b")
	destinationA := M.ParseSocksaddrHostPort("a.example", 443)
	destinationB := M.ParseSocksaddrHostPort("b.example", 443)
	inner := &testDestinationGroup{
		testGroup:     testGroup{testOutbound: newTestOutbound("hash"), selected: a},
		byDestination: map[string]adapter.Outbound{destinationA.String(): a, destinationB.String(): b},
	}
	outer := &testGroup{testOutbound: newTestOutbound("select"), selected: inner}

	chain, err := resolveOutbound(outer, N.NetworkTCP, destinationB)
	require.NoError(t, err)
	require.Equal(t, []adapter.Outbound{outer, inner, b}, chain)

	chain, err = resolveOutbound(outer, N.NetworkUDP, destinationA)
	require.NoError(t, err)
	require.Equal(t, a, chain[len(chain)-1])
}
