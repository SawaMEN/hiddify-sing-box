package pingtunnel

import (
	"sync/atomic"
	"time"
)

type atomicInt struct{ v atomic.Int64 }

func (a *atomicInt) Load() int   { return int(a.v.Load()) }
func (a *atomicInt) Store(v int) { a.v.Store(int64(v)) }
func (a *atomicInt) Add(v int)   { a.v.Add(int64(v)) }

type atomicTime struct{ v atomic.Int64 }

func newAtomicTime(t time.Time) *atomicTime { a := &atomicTime{}; a.Store(t); return a }
func (a *atomicTime) Load() time.Time {
	if a == nil {
		return time.Time{}
	}
	return time.Unix(0, a.v.Load())
}
func (a *atomicTime) Store(v time.Time) { a.v.Store(v.UnixNano()) }
