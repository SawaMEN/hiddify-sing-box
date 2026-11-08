package xhttp

import (
	"bytes"
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
)

type xuiCloseCountReader struct {
	io.Reader
	closes atomic.Int32
}

func (r *xuiCloseCountReader) Close() error {
	r.closes.Add(1)
	return nil
}

func TestXUIWaitReadCloserRejectsNilBody(t *testing.T) {
	w := &WaitReadCloser{ctx: context.Background(), Wait: make(chan struct{})}
	w.Set(nil)
	select {
	case <-w.Wait:
		t.Fatal("nil body must not publish a response")
	default:
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.Wait:
	default:
		t.Fatal("closing the waiter must wake readers")
	}
	if _, err := w.Read(make([]byte, 1)); err != io.ErrClosedPipe {
		t.Fatalf("Read after Close: got %v, want io.ErrClosedPipe", err)
	}
}

func TestXUIWaitReadCloserSetCloseRace(t *testing.T) {
	for i := 0; i < 128; i++ {
		w := &WaitReadCloser{ctx: context.Background(), Wait: make(chan struct{})}
		first := &xuiCloseCountReader{Reader: bytes.NewReader([]byte("one"))}
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			w.Set(first)
		}()
		go func() {
			defer group.Done()
			if err := w.Close(); err != nil {
				t.Errorf("close: %v", err)
			}
		}()
		group.Wait()
		select {
		case <-w.Wait:
		default:
			t.Fatal("Close or Set did not release waiter")
		}
		if got := first.closes.Load(); got != 1 {
			t.Fatalf("first body closed %d times, want exactly once", got)
		}
		late := &xuiCloseCountReader{Reader: bytes.NewReader([]byte("two"))}
		w.Set(late)
		if got := late.closes.Load(); got != 1 {
			t.Fatalf("late body closed %d times, want exactly once", got)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if got := first.closes.Load(); got != 1 {
			t.Fatalf("repeat Close closed first body %d times", got)
		}
	}
}
