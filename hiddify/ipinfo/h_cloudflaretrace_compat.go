package ipinfo

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// fetchBody provides the raw-body transport required by the upstream Cloudflare
// trace provider. It deliberately leaves the fork's existing JSON fetchData
// implementation untouched, including custom IP information providers.
func (p *BaseProvider) fetchBody(ctx context.Context, detour N.Dialer) ([]byte, uint16, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
	if err != nil {
		return nil, 65535, err
	}
	agent := p.UserAgent
	if agent == "" {
		agent = C.DefaultBrowserAgent
	}
	request.Header.Set("User-Agent", agent)

	transport := &http.Transport{
		TLSHandshakeTimeout: 15 * time.Second,
	}
	if detour != nil {
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			return detour.DialContext(ctx, network, M.ParseSocksaddrHostPortStr(host, port))
		}
	}
	defer transport.CloseIdleConnections()

	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	start := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return nil, 65535, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, 65535, fmt.Errorf("non-200 response from [%s]: %d", p.URL, response.StatusCode)
	}

	const maxResponseSize = 32 << 10
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil {
		return nil, 65535, fmt.Errorf("failed to read response from [%s]: %w", p.URL, err)
	}
	if len(body) > maxResponseSize {
		return nil, 65535, fmt.Errorf("response from [%s] exceeds %d bytes", p.URL, maxResponseSize)
	}
	ms := time.Since(start) / time.Millisecond
	if ms >= 65535 {
		ms = 65534
	}
	return body, uint16(ms), nil
}
