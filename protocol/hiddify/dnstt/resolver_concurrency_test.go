package dnstt

import (
	"sync"
	"testing"
)

func TestConcurrentResolverInitializationPublishesImmutableTables(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			loadResolvers()
			if len(countryResolvers) == 0 || len(resolverCountry) == 0 {
				t.Error("resolver table was not published")
			}
		}()
	}
	wg.Wait()
}
