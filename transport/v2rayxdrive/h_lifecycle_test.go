package v2rayxdrive

import (
	"context"
	"github.com/stretchr/testify/require"
	"net"
	"testing"
)

func TestH_ClientCloseOwnsActiveConnections(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{ctx: ctx, cancel: cancel, connections: make(map[*hTrackedConn]struct{})}
	a, b := net.Pipe()
	defer b.Close()
	conn, err := c.track(a)
	require.NoError(t, err)
	require.NoError(t, c.Close())
	require.NoError(t, c.Close())
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	_, err = conn.Write([]byte("closed"))
	require.Error(t, err)
	require.Empty(t, c.connections)
	a2, b2 := net.Pipe()
	defer b2.Close()
	_, err = c.track(a2)
	require.ErrorIs(t, err, net.ErrClosed)
	_, err = a2.Write([]byte("late dial"))
	require.Error(t, err)
}
