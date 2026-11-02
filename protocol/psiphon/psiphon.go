package psiphon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"

	"github.com/sagernet/sing-box/common/monitoring"
	"github.com/sagernet/sing/common/logger"

	"github.com/Psiphon-Labs/psiphon-tunnel-core/psiphon"
)

var Countries = []string{
	"AT",
	"AU",
	"BE",
	"BG",
	"CA",
	"CH",
	"CZ",
	"DE",
	"DK",
	"EE",
	"ES",
	"FI",
	"FR",
	"GB",
	"HR",
	"HU",
	"IE",
	"IN",
	"IT",
	"JP",
	"LV",
	"NL",
	"NO",
	"PL",
	"PT",
	"RO",
	"RS",
	"SE",
	"SG",
	"SK",
	"US",
}

// NoticeEvent represents the notices emitted by tunnel core. It will be passed to
// noticeReceiver, if supplied.
// NOTE: Ordinary users of this library should never need this.
type NoticeEvent struct {
	Data      map[string]any `json:"data"`
	Type      string         `json:"noticeType"`
	Timestamp string         `json:"timestamp"`
}

type Psiphon struct {
	mu              sync.RWMutex
	closeMu         sync.Mutex
	controller      *psiphon.Controller
	logger          logger.ContextLogger
	config          *psiphon.Config
	ctx             context.Context
	cancel          context.CancelFunc
	dataStoreOpened bool
	connected       atomic.Bool
	closed          bool
	startDone       chan struct{}
	runDone         chan struct{}
	tag             string
}

func (p *Psiphon) Dial(address string, conn net.Conn) (net.Conn, error) {
	p.mu.RLock()
	controller, closed := p.controller, p.closed
	p.mu.RUnlock()
	if closed {
		return nil, net.ErrClosed
	}
	if controller == nil {
		return nil, errors.New("controller not initialized")
	}
	return controller.Dial(address, conn)
}

func (p *Psiphon) PreStart() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return net.ErrClosed
	}
	if p.dataStoreOpened {
		return nil
	}
	if err := os.MkdirAll(p.config.DataRootDirectory, 0o700); err != nil {
		return err
	}
	if err := p.config.Commit(true); err != nil {
		return err
	}
	if err := psiphon.OpenDataStore(p.config); err != nil {
		return err
	}
	p.dataStoreOpened = true
	if err := psiphon.ImportEmbeddedServerEntries(p.ctx, p.config, "", ""); err != nil {
		psiphon.CloseDataStore()
		p.dataStoreOpened = false
		return err
	}
	return nil
}

func (p *Psiphon) State() string {
	if p.IsConnected() {
		return "connected"
	}
	return "connecting..."
}

func (p *Psiphon) IsConnected() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return !p.closed && p.controller != nil && p.connected.Load()
}

func (p *Psiphon) NetworkChanged() {
	p.mu.RLock()
	controller, closed := p.controller, p.closed
	p.mu.RUnlock()
	if controller != nil && !closed {
		controller.NetworkChanged()
	}
}

func (p *Psiphon) Close() error {
	p.closeMu.Lock()
	defer p.closeMu.Unlock()
	p.mu.Lock()
	p.closed = true
	p.connected.Store(false)
	if p.cancel != nil {
		p.cancel()
	}
	startDone := p.startDone
	p.mu.Unlock()
	stopListeningNotices(p)
	// Cancellation releases Start's wait before data-store shutdown. It also
	// prevents a controller being published after Close has already returned.
	if startDone != nil {
		<-startDone
	}
	p.mu.RLock()
	runDone := p.runDone
	p.mu.RUnlock()
	if runDone != nil {
		<-runDone
	}
	p.mu.Lock()
	if p.dataStoreOpened {
		psiphon.CloseDataStore()
		p.dataStoreOpened = false
	}
	p.controller = nil
	p.mu.Unlock()
	return nil
}

func NewPsiphon(ctx context.Context, l logger.ContextLogger, config *psiphon.Config, tag string) (*Psiphon, error) {
	ctx, cancel := context.WithCancel(ctx)
	return &Psiphon{logger: l, config: config, ctx: ctx, cancel: cancel, tag: tag}, nil
}

func (p *Psiphon) Start() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return net.ErrClosed
	}
	if p.startDone != nil {
		p.mu.Unlock()
		return errors.New("Psiphon already started")
	}
	if !p.dataStoreOpened {
		p.mu.Unlock()
		return errors.New("Psiphon not prepared")
	}
	p.startDone = make(chan struct{})
	startDone := p.startDone
	p.mu.Unlock()
	defer close(startDone)

	connected := make(chan struct{}, 1)
	errored := make(chan error, 1)
	if err := listenNotices(p, func(event NoticeEvent) {
		switch event.Type {
		case "EstablishTunnelTimeout":
			select {
			case errored <- errors.New("Psiphon tunnel establishment timeout"):
			default:
			}
		case "Tunnels":
			p.onNotice(event, func() {
				select {
				case connected <- struct{}{}:
				default:
				}
			})
		}
	}); err != nil {
		return err
	}
	controller, err := psiphon.NewController(p.config)
	if err != nil {
		return fmt.Errorf("Psiphon controller: %w", err)
	}
	runDone := make(chan struct{})
	p.mu.Lock()
	p.controller, p.runDone = controller, runDone
	p.mu.Unlock()
	go func() {
		defer close(runDone)
		controller.Run(p.ctx)
		p.mu.Lock()
		p.connected.Store(false)
		p.mu.Unlock()
		select {
		case errored <- errors.New("Psiphon controller stopped"):
		default:
		}
	}()
	select {
	case <-p.ctx.Done():
		return p.ctx.Err()
	case <-connected:
		if err := p.ctx.Err(); err != nil {
			return err
		}
		return nil
	case err := <-errored:
		return err
	}
}

// func RunPsiphon(ctx context.Context, l logger.ContextLogger, wgBind netip.AddrPort, dir string, localSocksAddr netip.AddrPort, country string) error {
// 	host := ""
// 	if !netip.MustParsePrefix("127.0.0.0/8").Contains(localSocksAddr.Addr()) {
// 		host = "any"
// 	}

// 	timeout := 60
// 	config := psiphon.Config{
// 		EgressRegion:                                 country,
// 		ListenInterface:                              host,
// 		LocalSocksProxyPort:                          int(localSocksAddr.Port()),
// 		UpstreamProxyURL:                             fmt.Sprintf("socks5://%s", wgBind),
// 		DisableLocalHTTPProxy:                        true,
// 		PropagationChannelId:                         "FFFFFFFFFFFFFFFF",
// 		RemoteServerListDownloadFilename:             "remote_server_list",
// 		RemoteServerListSignaturePublicKey:           "MIICIDANBgkqhkiG9w0BAQEFAAOCAg0AMIICCAKCAgEAt7Ls+/39r+T6zNW7GiVpJfzq/xvL9SBH5rIFnk0RXYEYavax3WS6HOD35eTAqn8AniOwiH+DOkvgSKF2caqk/y1dfq47Pdymtwzp9ikpB1C5OfAysXzBiwVJlCdajBKvBZDerV1cMvRzCKvKwRmvDmHgphQQ7WfXIGbRbmmk6opMBh3roE42KcotLFtqp0RRwLtcBRNtCdsrVsjiI1Lqz/lH+T61sGjSjQ3CHMuZYSQJZo/KrvzgQXpkaCTdbObxHqb6/+i1qaVOfEsvjoiyzTxJADvSytVtcTjijhPEV6XskJVHE1Zgl+7rATr/pDQkw6DPCNBS1+Y6fy7GstZALQXwEDN/qhQI9kWkHijT8ns+i1vGg00Mk/6J75arLhqcodWsdeG/M/moWgqQAnlZAGVtJI1OgeF5fsPpXu4kctOfuZlGjVZXQNW34aOzm8r8S0eVZitPlbhcPiR4gT/aSMz/wd8lZlzZYsje/Jr8u/YtlwjjreZrGRmG8KMOzukV3lLmMppXFMvl4bxv6YFEmIuTsOhbLTwFgh7KYNjodLj/LsqRVfwz31PgWQFTEPICV7GCvgVlPRxnofqKSjgTWI4mxDhBpVcATvaoBl1L/6WLbFvBsoAUBItWwctO2xalKxF5szhGm8lccoc5MZr8kfE0uxMgsxz4er68iCID+rsCAQM=",
// 		RemoteServerListUrl:                          "https://s3.amazonaws.com//psiphon/web/mjr4-p23r-puwl/server_list_compressed",
// 		SponsorId:                                    "FFFFFFFFFFFFFFFF",
// 		NetworkID:                                    "test",
// 		ClientPlatform:                               "Android_4.0.4_com.example.exampleClientLibraryApp",
// 		AllowDefaultDNSResolverWithBindToDevice:      true,
// 		EstablishTunnelTimeoutSeconds:                &timeout,
// 		DataRootDirectory:                            dir,
// 		MigrateDataStoreDirectory:                    dir,
// 		MigrateObfuscatedServerListDownloadDirectory: dir,
// 		MigrateRemoteServerListDownloadFilename:      filepath.Join(dir, "server_list_compressed"),
// 	}

// 	l.Info("starting handshake")
// 	if _, err := StartTunnel(ctx, l, &config); err != nil {
// 		return fmt.Errorf("Unable to start psiphon: %w", err)
// 	}
// 	l.Info("psiphon started successfully")
// 	return nil
// }

// setConnected updates the current connection state independently of the notice dispatcher.
func (p *Psiphon) setConnected(connected bool) {
	p.mu.RLock()
	closed := p.closed
	p.mu.RUnlock()
	if !closed {
		p.connected.Store(connected)
	}
}

// onNotice receives events via the process-wide writer. A notice does not
// identify its controller, so unstarted instances only claim a notice while
// they are the sole listener.
func (p *Psiphon) onNotice(event NoticeEvent, notify func()) {
	if event.Type != "Tunnels" {
		return
	}
	count, ok := noticeCount(event.Data)
	if !ok {
		return
	}
	p.mu.RLock()
	closed, controller := p.closed, p.controller
	p.mu.RUnlock()
	if closed {
		return
	}
	noticeHub.access.Lock()
	soleListener := len(noticeHub.listeners) <= 1
	noticeHub.access.Unlock()
	if controller == nil && !soleListener {
		return
	}
	p.setConnected(count > 0)
	if count > 0 {
		if notify != nil {
			notify()
		}
		if monitor := monitoring.Get(p.ctx); monitor != nil {
			_ = monitor.TestNow(p.tag)
		}
	}
}
