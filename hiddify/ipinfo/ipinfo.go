package ipinfo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

var providers = []Provider{
	NewFreeIpApiProvider(),
	NewIpApiCoProvider(),
	NewIpApiProvider(),
	NewIpInfoIoProvider(),
	NewIpSbProvider(),
	NewIpWhoIsProvider(),
	// NewCloudflareTraceProvider(),
	NewCountryIsProvider(),
	NewMyIPExpertProvider(),
	NewMyIPioProvider(),
	NewReallyFreeGeoIPProvider(),
}

// fallbackProviders are tried when every provider above failed. The providers are tried from a
// random one, so a place in the list is no order; Cloudflare's trace endpoint is here, not
// above, because it answers only the address and the country: the JSON providers also give the
// network, which the app shows, so they come first, and the trace answers when they rate-limit
// (429) or time out on a busy network and the core would otherwise have no measurement at all.
var fallbackProviders = []Provider{
	NewMyIPProvider(),
	NewCountryIsProvider(),
	// NewCloudflareTraceProvider(),
}

func GetAllIPCheckerDomainsDomains() []string {
	var domains []string
	for _, provider := range providers {
		d, err := url.Parse(provider.GetName())
		if err == nil {
			if net.ParseIP(d.Hostname()) == nil {
				domains = append(domains, d.Hostname())
			}
		}
	}
	return domains
}

// Provider interface for all IP providers.
type Provider interface {
	GetIPInfo(ctx context.Context, dialer N.Dialer) (*IpInfo, uint16, error)
	GetName() string
}

// IpInfo stores the IP information from the API response.
type IpInfo struct {
	IP          string  `json:"ip"`
	CountryCode string  `json:"country_code"`
	Region      string  `json:"region,omitempty"`
	City        string  `json:"city,omitempty"`
	ASN         int     `json:"asn,omitempty"`
	Org         string  `json:"org,omitempty"`
	Latitude    float64 `json:"latitude,omitempty"`
	Longitude   float64 `json:"longitude,omitempty"`
	PostalCode  string  `json:"postal_code,omitempty"`
}

func (ip *IpInfo) String() string {
	return fmt.Sprintf("IP: %s, Country: %s, Region: %s, City: %s, ASN: %d, Org: %s, Latitude: %.6f, Longitude: %.6f, Postal Code: %s",
		ip.IP, ip.CountryCode, ip.Region, ip.City, ip.ASN, ip.Org, ip.Latitude, ip.Longitude, ip.PostalCode)
}

// BaseProvider struct to handle common logic (HTTP request).
type BaseProvider struct {
	URL       string
	UserAgent string
}

func (p *BaseProvider) GetName() string {
	return p.URL
}

// fetchData retrieves the data from the provider's URL with a custom user agent and dialer.
func (p *BaseProvider) fetchData(ctx context.Context, detour N.Dialer) (map[string]any, uint16, error) {
	link := p.URL
	linkURL, err := url.Parse(link)
	if err != nil {
		return nil, 65535, err
	}
	hostname := linkURL.Hostname()
	port := linkURL.Port()
	if port == "" {
		switch linkURL.Scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}

	req, err := http.NewRequest(http.MethodGet, link, nil)
	if err != nil {
		return nil, 65535, err
	}
	agent := p.UserAgent //H: provider is shared by concurrent monitoring tests
	if agent == "" {
		agent = C.DefaultBrowserAgent
	}
	req.Header.Set("User-Agent", agent)
	start := time.Now()
	var client http.Client
	if detour != nil {
		instance, err := detour.DialContext(ctx, "tcp", M.ParseSocksaddrHostPortStr(hostname, port))
		if err != nil {
			return nil, 65535, err
		}
		defer instance.Close()
		if N.NeedHandshakeForWrite(instance) { // H
			start = time.Now()
		}

		client = http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return instance, nil
				},
			},
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	} else {
		client = http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return nil, 65535, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, 65535, fmt.Errorf("non-200 response from [%s]: %d", p.URL, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 65535, fmt.Errorf("failed to read response from [%s]: %v", p.URL, err)
	}
	t := uint16(time.Since(start) / time.Millisecond)
	var jsonResponse map[string]any
	err = json.Unmarshal(body, &jsonResponse)
	if err != nil {
		return nil, 65535, fmt.Errorf("failed to parse JSON from [%s]: %v", p.URL, err)
	}

	return jsonResponse, t, nil
}

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

// getCurrentIpInfo iterates over the providers to fetch and parse IP information.
func GetIpInfo(logger log.Logger, ctx context.Context, detour N.Dialer) (*IpInfo, uint16, error) {
	var lastErr error
	startIndex := rand.Intn(len(providers))
	for i := 0; i < len(providers); i++ {
		select {
		case <-ctx.Done():
			return nil, 65535, ctx.Err()
		default:
		}
		provider := providers[(i+startIndex)%len(providers)]
		testCtx, cancel := context.WithTimeout(ctx, C.TCPTimeout)
		ipInfo, t, err := provider.GetIPInfo(testCtx, detour)
		cancel()
		if err != nil {
			logger.Warn("Failed try ", i, " to get IP info: ", provider.GetName(), " ", err)
			lastErr = err
			continue
		}
		return ipInfo, t, nil
	}
	startIndex = rand.Intn(len(fallbackProviders))
	for i := 0; i < len(fallbackProviders); i++ {
		select {
		case <-ctx.Done():
			return nil, 65535, ctx.Err()
		default:
		}
		provider := fallbackProviders[(i+startIndex)%len(fallbackProviders)]
		testCtx, cancel := context.WithTimeout(ctx, C.TCPTimeout)
		ipInfo, t, err := provider.GetIPInfo(testCtx, detour)
		cancel()
		if err != nil {
			logger.Warn("Failed try ", i, " to get IP info: ", provider.GetName(), " ", err)
			continue
		}
		return ipInfo, t, nil
	}
	return nil, 65535, fmt.Errorf("unable to retrieve IP info: %v", lastErr)
}

// func init() {
// 	// Instantiate the providers.

// 	for _, provider := range providers {
// 		x, _, err := provider.GetIPInfo(context.Background(), nil)
// 		fmt.Printf("%s:   %++v\n%++v\n", provider, x, err)
// 	}
// 	// Get IP information.

// 	os.Exit(0)
// }

// GetIpInfoFromURL retrieves IP information from a custom provider through the outbound. //H
func GetIpInfoFromURL(ctx context.Context, detour N.Dialer, url string) (*IpInfo, uint16, error) {
	data, elapsed, err := (&BaseProvider{URL: url}).fetchData(ctx, detour)
	if err != nil {
		return nil, elapsed, err
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, elapsed, err
	}
	var info IpInfo
	if err = json.Unmarshal(raw, &info); err != nil {
		return nil, elapsed, err
	}
	if info.IP == "" {
		return nil, elapsed, fmt.Errorf("IP provider %s did not supply an IP", url)
	}
	return &info, elapsed, nil
}
