package route

import (
	"testing"

	"github.com/sagernet/sing-tun"
	"github.com/stretchr/testify/require"
)

func TestFlowReleaseTrackerReleasesNestedGroupsOnce(t *testing.T) {
	var releaseOrder []int
	tracker := newFlowReleaseTracker([]func() func(){
		func() func() { return func() { releaseOrder = append(releaseOrder, 1) } },
		func() func() { return func() { releaseOrder = append(releaseOrder, 2) } },
	})

	tracker.CloseFlow(tun.FlowCloseFinished)
	tracker.CloseFlow(tun.FlowCloseReset)
	require.Equal(t, []int{2, 1}, releaseOrder)
}

func TestFlowReleaseTrackerCommitsSelectionWithoutRelease(t *testing.T) {
	var acquisitions int
	tracker := newFlowReleaseTracker([]func() func(){
		func() func() { acquisitions++; return nil },
	})
	require.Equal(t, 1, acquisitions)
	tracker.CloseFlow(tun.FlowCloseFinished)
	tracker.CloseFlow(tun.FlowCloseReset)
	require.Equal(t, 1, acquisitions)
}
