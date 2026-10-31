package powerreport

// hInterfaceCounterDelta ignores resets (for example when an interface is
// re-created) rather than reporting a wrapped, near-uint64-max delta.
func hInterfaceCounterDelta(current uint64, previous uint64) uint64 {
	if current < previous {
		return 0
	}
	return current - previous
}
