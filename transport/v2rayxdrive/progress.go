// SPDX-License-Identifier: MPL-2.0
package xdrive

import (
	"context"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/utils"
	"io"
	"net/http"
	"time"
)

var errStalled = errors.New("the request made no progress")

type serviceTransport struct {
	plain  http.RoundTripper
	secure http.RoundTripper
	stall  time.Duration
}

// RoundTrip ends a request that stands still for t.stall, with no answer that
// long after its body went out or no byte of either body, but not one that just
// runs long: a large object on a slow link takes minutes and is fine as long as
// its bytes move
func (t *serviceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	next := t.plain
	if r.URL.Scheme == "https" {
		next = t.secure
	}

	ctx, cancel := context.WithCancelCause(r.Context())
	timer := time.AfterFunc(t.stall, func() { cancel(errStalled) })
	alive := func() { timer.Reset(t.stall) }
	end := func() {
		timer.Stop()
		cancel(nil)
	}
	r = r.WithContext(ctx)
	// the headers of chrome as with XHTTP and DoH, net/http and http3 would send
	// their own user agent after a hello of chrome
	r.Header = r.Header.Clone()
	utils.TryDefaultHeadersWith(r.Header, "fetch")
	if r.Body != nil && r.Body != http.NoBody {
		r.Body = &progressBody{ReadCloser: r.Body, alive: alive}
	}
	// a body sent again by the transport itself, after a GOAWAY or on a reused
	// connection that failed, moves just the same
	if getBody := r.GetBody; getBody != nil {
		r.GetBody = func() (io.ReadCloser, error) {
			body, err := getBody()
			if err != nil || body == http.NoBody {
				return body, err
			}
			return &progressBody{ReadCloser: body, alive: alive}, nil
		}
	}

	resp, err := next.RoundTrip(r)
	if err != nil {
		err = stalled(ctx, err)
		end()
		return nil, err
	}
	alive()
	resp.Body = &progressBody{ReadCloser: resp.Body, alive: alive, end: end, ctx: ctx}
	return resp, nil
}

// stalled returns errStalled for an error that the stall gave, which http2 reports
// as a plain cancel
func stalled(ctx context.Context, err error) error {
	if context.Cause(ctx) == errStalled {
		return errStalled
	}
	return err
}

type progressBody struct {
	io.ReadCloser
	alive func()
	end   func()
	// ctx is that of the request, for the body of its response
	ctx context.Context
}

func (b *progressBody) Read(p []byte) (int, error) {
	// http2 reads a request body up to 512 KiB at a time and sends it all before
	// the next read, which on a slow link could look like no progress
	n, err := b.ReadCloser.Read(p[:min(len(p), 16<<10)])
	if n > 0 {
		b.alive()
	}
	if err != nil && err != io.EOF && b.ctx != nil {
		err = stalled(b.ctx, err)
	}
	return n, err
}

func (b *progressBody) Close() error {
	err := b.ReadCloser.Close()
	if b.end != nil {
		b.end()
	}
	return err
}

// newServiceClient returns a client whose requests end after stall without
// progress, see serviceTransport.RoundTrip, and whose connections close when
// connCtx is done. Clients that share h1Hosts share which hosts answer over h1
