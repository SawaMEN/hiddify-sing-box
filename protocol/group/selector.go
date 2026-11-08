package group

import (
	"context"
	"io"
	"net"
	"slices"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/interrupt"
	"github.com/sagernet/sing-box/common/monitoring"
	"github.com/sagernet/sing-box/common/urltest"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

func RegisterSelector(registry *outbound.Registry) {
	outbound.Register[option.SelectorOutboundOptions](registry, C.TypeSelector, NewSelector)
}

var (
	_ adapter.OutboundGroup = (*Selector)(nil)
	_ adapter.Referrer      = (*Selector)(nil)
)

type Selector struct {
	outbound.Adapter
	ctx                          context.Context
	outbound                     adapter.OutboundManager
	logger                       logger.ContextLogger
	tags                         []string
	defaultTag                   string
	outbounds                    map[string]adapter.Outbound
	selected                     common.TypedValue[adapter.Outbound]
	balance                      *balanceState
	history                      *urltest.HistoryStorage
	interruptGroup               *interrupt.Group
	interruptExternalConnections bool
}

func NewSelector(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.SelectorOutboundOptions) (adapter.Outbound, error) {
	var err error
	outbound := &Selector{
		Adapter:                      outbound.NewAdapter(C.TypeSelector, tag, nil, selectorDependencies(options)),
		ctx:                          ctx,
		outbound:                     service.FromContext[adapter.OutboundManager](ctx),
		logger:                       logger,
		tags:                         options.Outbounds,
		defaultTag:                   options.Default,
		outbounds:                    make(map[string]adapter.Outbound),
		history:                      service.PtrFromContext[urltest.HistoryStorage](ctx),
		interruptGroup:               interrupt.NewGroup(),
		interruptExternalConnections: options.InterruptExistConnections,
	}
	if len(outbound.tags) == 0 {
		return nil, E.New("missing tags")
	}
	for i, tag := range outbound.tags {
		if slices.Contains(outbound.tags[:i], tag) {
			return nil, E.New("duplicate tag: ", tag)
		}
	}
	if options.Balancer != nil {
		if options.Balancer.Fallback == tag && tag != "" {
			return nil, E.New("balancer cannot reference itself as fallback")
		}
		outbound.balance, err = newBalanceState(ctx, *options.Balancer)
		if err != nil {
			return nil, err
		}
	}
	return outbound, nil
}

func selectorDependencies(options option.SelectorOutboundOptions) []string {
	dependencies := append([]string(nil), options.Outbounds...)
	if options.Balancer != nil && options.Balancer.Fallback != "" && !slices.Contains(dependencies, options.Balancer.Fallback) {
		dependencies = append(dependencies, options.Balancer.Fallback)
	}
	return dependencies
}

func (s *Selector) Network() []string {
	if s.balance != nil {
		return []string{N.NetworkTCP, N.NetworkUDP}
	}
	selected := s.selected.Load()
	if selected == nil {
		return []string{N.NetworkTCP, N.NetworkUDP}
	}
	return selected.Network()
}

func (s *Selector) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage == adapter.StartStatePostStart {
		if s.balance != nil {
			s.balance.postStart(s.outbounds)
		}
		return nil
	}
	if stage != adapter.StartStateStart {
		return nil
	}
	for i, tag := range s.tags {
		detour, loaded := s.outbound.Outbound(tag)
		if !loaded {
			return E.New("outbound ", i, " not found: ", tag)
		}
		s.outbounds[tag] = detour
	}
	if s.balance != nil {
		if fallback := s.balance.config.Fallback; fallback != "" {
			detour, loaded := s.outbound.Outbound(fallback)
			if !loaded {
				return E.New("balancer fallback outbound not found: ", fallback)
			}
			s.outbounds[fallback] = detour
		}
		s.selected.Store(s.outbounds[s.tags[0]])
		return nil
	}

	if s.Tag() != "" {
		cacheFile := service.FromContext[adapter.CacheFile](s.ctx)
		if cacheFile != nil {
			selected := cacheFile.LoadSelected(s.Tag())
			if selected != "" {
				detour, loaded := s.outbounds[selected]
				if loaded {
					s.selected.Store(detour)
					return nil
				}
			}
		}
	}

	if s.defaultTag != "" {
		detour, loaded := s.outbounds[s.defaultTag]
		if !loaded {
			return E.New("default outbound not found: ", s.defaultTag)
		}
		s.selected.Store(detour)
		return nil
	}

	s.selected.Store(s.outbounds[s.tags[0]])
	return nil
}

func (s *Selector) All() []string {
	return s.tags
}

func (s *Selector) Selected(network string) adapter.Outbound {
	if s.balance != nil {
		tag, err := s.balance.pick(s.tags)
		if err != nil {
			return nil
		}
		return s.outbounds[tag]
	}
	return s.selected.Load()
}

func (s *Selector) AttachConnection(closer io.Closer) func() {
	return s.interruptGroup.Add(closer, true)
}

func (s *Selector) References() []string {
	if s.balance != nil {
		return selectorDependencies(option.SelectorOutboundOptions{Outbounds: s.tags, Balancer: &s.balance.config})
	}
	selected := s.selected.Load()
	if selected == nil {
		return s.tags[:1]
	}
	return []string{selected.Tag()}
}

func (s *Selector) SelectOutbound(tag string) bool {
	if s.balance != nil {
		return false
	}
	detour, loaded := s.outbounds[tag]
	if !loaded {
		return false
	}
	if s.selected.Swap(detour) == detour {
		return true
	}
	if s.Tag() != "" {
		cacheFile := service.FromContext[adapter.CacheFile](s.ctx)
		if cacheFile != nil {
			err := cacheFile.StoreSelected(s.Tag(), tag)
			if err != nil {
				s.logger.Error("store selected: ", err)
			}
		}
	}
	s.interruptGroup.Interrupt(s.interruptExternalConnections)
	if s.history != nil {
		s.history.NotifyUpdated()
	}
	// H: a selection changes this group and everything routed through it
	if mon := monitoring.Get(s.ctx); mon != nil && s.Tag() != "" {
		if err := mon.SignalChange(s.Tag()); err != nil {
			s.logger.Debug("signal selection change: ", err)
		}
	}
	return true
}

func (s *Selector) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	detour := s.Selected(network)
	if detour == nil {
		return nil, E.New("balancer has no healthy outbound")
	}
	conn, err := detour.DialContext(ctx, network, destination)
	if err != nil {
		return nil, err
	}
	return s.interruptGroup.NewConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
}

func (s *Selector) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	detour := s.Selected(N.NetworkUDP)
	if detour == nil {
		return nil, E.New("balancer has no healthy outbound")
	}
	conn, err := detour.ListenPacket(ctx, destination)
	if err != nil {
		return nil, err
	}
	return s.interruptGroup.NewPacketConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
}

func (s *Selector) Close() error {
	if s.balance != nil {
		return s.balance.Close()
	}
	return nil
}

func RealTag(detour adapter.Outbound, network string) string {
	for {
		group, isGroup := detour.(adapter.OutboundGroup)
		if !isGroup {
			return detour.Tag()
		}
		detour = group.Selected(network)
		if detour == nil {
			return ""
		}
	}
}
