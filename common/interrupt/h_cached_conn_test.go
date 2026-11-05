package interrupt_test

import (
	"io"
	"net"
	"sync"
	"testing"

	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
)

func TestH_CachedConnCloseWhileDraining(t *testing.T) {
	for _, writeTo := range []bool{false, true} {
		for range 2000 {
			client, server := net.Pipe()
			buffer := buf.NewSize(64)
			_, _ = buffer.WriteString("cached")
			conn := bufio.NewCachedConn(client, buffer)
			buffer.Release()
			var wait sync.WaitGroup
			wait.Go(func() {
				if writeTo {
					_, _ = conn.WriteTo(io.Discard)
				} else {
					p := make([]byte, 64)
					for {
						if _, err := conn.Read(p); err != nil {
							return
						}
					}
				}
			})
			_ = conn.Close()
			_ = server.Close()
			wait.Wait()
		}
	}
}
