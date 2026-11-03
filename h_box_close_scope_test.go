package box

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
)

type hScopeLogger struct {
	log.Factory
	closes  atomic.Int32
	failure error
}

func (l *hScopeLogger) Close() error {
	if l.closes.Add(1) != 1 {
		return os.ErrClosed
	}
	return l.failure
}

func hScopedBoxLogger(failure error) (*Box, *hScopeLogger) {
	logger := &hScopeLogger{Factory: log.NewNOPFactory(), failure: failure}
	scope := adapter.NewScope(context.Background(), logger.Logger())
	scope.Add(logger.Close)
	return &Box{scope: scope, logFactory: logger, done: make(chan struct{})}, logger
}

func TestH_BoxCloseCallsScopeLoggerOnce(t *testing.T) {
	instance, logger := hScopedBoxLogger(nil)
	if err := instance.Close(); err != nil {
		t.Fatal(err)
	}
	if count := logger.closes.Load(); count != 1 {
		t.Fatalf("logger closed %d times", count)
	}
	if !errors.Is(instance.Close(), os.ErrClosed) {
		t.Fatal("repeat box close lost its existing contract")
	}
	if count := logger.closes.Load(); count != 1 {
		t.Fatalf("repeat close closed logger %d times", count)
	}
}

func TestH_BoxClosePreservesScopeLoggerFailure(t *testing.T) {
	failure := errors.New("test logger cleanup failure")
	instance, logger := hScopedBoxLogger(failure)
	if err := instance.Close(); !errors.Is(err, failure) {
		t.Fatalf("cleanup failure lost: %v", err)
	}
	if count := logger.closes.Load(); count != 1 {
		t.Fatalf("failing logger closed %d times", count)
	}
}
