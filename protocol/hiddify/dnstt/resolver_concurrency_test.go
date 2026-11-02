package dnstt

import (
	"sync"
	"testing"
)

func TestConcurrentResolverInitializationPublishesImmutableTables(t *testing.T) {
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			loadResolvers()
			if len(countryResolvers) == 0 || len(resolverCountry) == 0 {
				t.Error("resolver table was not published")
			}
		})
	}
	wg.Wait()
}
