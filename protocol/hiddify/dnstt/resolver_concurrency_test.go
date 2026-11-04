package dnstt

import (
	"sync"
	"testing"

	"github.com/sagernet/sing-box/common/dnspool"
)

func TestConcurrentResolverInitializationPublishesImmutableTables(t *testing.T) {
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			dnspool.BuiltinResolvers()
			if len(dnspool.BuiltinResolversByCountry()) == 0 || len(dnspool.BuiltinResolvers()) == 0 {
				t.Error("resolver table was not published")
			}
		})
	}
	wg.Wait()
}
