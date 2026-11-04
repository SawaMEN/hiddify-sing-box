package xhttp

import (
	"context"
	"io"
	"sync"
)

// WaitReadCloser publishes the response body after the HTTP handshake completes.
// Close may run before Set; in that case the arriving body is closed immediately.
type WaitReadCloser struct {
	ctx  context.Context
	Wait chan struct{}
	io.ReadCloser
	access sync.Mutex
	closed bool
}

func (w *WaitReadCloser) Set(rc io.ReadCloser) {
	w.access.Lock()
	if w.closed || w.ReadCloser != nil {
		w.access.Unlock()
		rc.Close()
		return
	}
	w.ReadCloser = rc
	close(w.Wait)
	w.access.Unlock()
}

func (w *WaitReadCloser) Read(b []byte) (int, error) {
	select {
	case <-w.ctx.Done():
		return 0, w.ctx.Err()
	case <-w.Wait:
	}
	w.access.Lock()
	rc, closed := w.ReadCloser, w.closed
	w.access.Unlock()
	if rc == nil || closed {
		return 0, io.ErrClosedPipe
	}
	return rc.Read(b)
}

func (w *WaitReadCloser) Close() error {
	w.access.Lock()
	if w.closed {
		w.access.Unlock()
		return nil
	}
	w.closed = true
	rc := w.ReadCloser
	if rc == nil {
		close(w.Wait)
	}
	w.access.Unlock()
	if rc != nil {
		return rc.Close()
	}
	return nil
}
