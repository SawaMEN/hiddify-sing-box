package dns

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
)

var _ adapter.DNSTransportManager = (*TransportManager)(nil)

type TransportManager struct {
	registry                 adapter.DNSTransportRegistry
	outbound                 adapter.OutboundManager
	defaultTag               string
	access                   sync.RWMutex
	transports               []adapter.DNSTransport
	transportByTag           map[string]adapter.DNSTransport
	defaultTransport         adapter.DNSTransport
	defaultTransportFallback func() (adapter.DNSTransport, error)
	fakeIPTransport          adapter.FakeIPTransport
	scope                    *adapter.Scope // H: where servers created by hot reload are started
	started                  bool
}

func NewTransportManager(registry adapter.DNSTransportRegistry, outbound adapter.OutboundManager, defaultTag string) *TransportManager {
	return &TransportManager{
		registry:       registry,
		outbound:       outbound,
		defaultTag:     defaultTag,
		transportByTag: make(map[string]adapter.DNSTransport),
	}
}

func (m *TransportManager) Initialize(defaultTransportFallback func() (adapter.DNSTransport, error)) {
	m.defaultTransportFallback = defaultTransportFallback
}

func (m *TransportManager) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	m.access.Lock()
	m.scope = scope
	if stage == adapter.StartStateInitialize {
		if m.defaultTag != "" && m.defaultTransport == nil {
			m.access.Unlock()
			return E.New("default DNS server not found: ", m.defaultTag)
		}
		if m.defaultTransport == nil {
			defaultTransport, err := m.defaultTransportFallback()
			if err != nil {
				m.access.Unlock()
				return E.Cause(err, "default DNS server fallback")
			}
			m.transports = append(m.transports, defaultTransport)
			m.transportByTag[defaultTransport.Tag()] = defaultTransport
			m.defaultTransport = defaultTransport
		}
	}
	transports := m.transports
	m.access.Unlock()
	if stage == adapter.StartStateStart {
		return m.startTransports(scope, transports)
	}
	for _, transport := range transports {
		name := "dns/" + transport.Type() + "[" + transport.Tag() + "]"
		err := scope.Start(name, transport, stage)
		if err != nil {
			return err
		}
	}
	if stage == adapter.StartStateStarted {
		m.access.Lock()
		m.started = true
		m.access.Unlock()
	}
	return nil
}

func (m *TransportManager) startTransports(scope *adapter.Scope, transports []adapter.DNSTransport) error {
	started := make(map[string]bool)
	for {
		canContinue := false
	startOne:
		for _, transportToStart := range transports {
			transportTag := transportToStart.Tag()
			if started[transportTag] {
				continue
			}
			dependencies := transportToStart.Dependencies()
			for _, dependency := range dependencies {
				if !started[dependency] {
					continue startOne
				}
			}
			started[transportTag] = true
			canContinue = true
			name := "dns/" + transportToStart.Type() + "[" + transportTag + "]"
			err := scope.Start(name, transportToStart, adapter.StartStateStart)
			if err != nil {
				return err
			}
		}
		if len(started) == len(transports) {
			break
		}
		if canContinue {
			continue
		}
		currentTransport := common.Find(transports, func(it adapter.DNSTransport) bool {
			return !started[it.Tag()]
		})
		var lintTransport func(oTree []string, oCurrent adapter.DNSTransport) error
		lintTransport = func(oTree []string, oCurrent adapter.DNSTransport) error {
			problemTransportTag := common.Find(oCurrent.Dependencies(), func(it string) bool {
				return !started[it]
			})
			if common.Contains(oTree, problemTransportTag) {
				return E.New("circular server dependency: ", strings.Join(oTree, " -> "), " -> ", problemTransportTag)
			}
			m.access.Lock()
			problemTransport := m.transportByTag[problemTransportTag]
			m.access.Unlock()
			if problemTransport == nil {
				return E.New("dependency[", problemTransportTag, "] not found for server[", oCurrent.Tag(), "]")
			}
			return lintTransport(append(oTree, problemTransportTag), problemTransport)
		}
		return lintTransport([]string{currentTransport.Tag()}, currentTransport)
	}
	return nil
}

func (m *TransportManager) Transports() []adapter.DNSTransport {
	m.access.RLock()
	defer m.access.RUnlock()
	return m.transports
}

func (m *TransportManager) Transport(tag string) (adapter.DNSTransport, bool) {
	m.access.RLock()
	outbound, found := m.transportByTag[tag]
	m.access.RUnlock()
	return outbound, found
}

func (m *TransportManager) Default() adapter.DNSTransport {
	m.access.RLock()
	defer m.access.RUnlock()
	return m.defaultTransport
}

func (m *TransportManager) FakeIP() adapter.FakeIPTransport {
	m.access.RLock()
	defer m.access.RUnlock()
	return m.fakeIPTransport
}

func (m *TransportManager) Remove(tag string) error {
	return m.remove(tag, false)
}

// RemoveForReload removes a server even while others still depend on it; used by hot reload,
// which recreates those dependents in the same reload. //H
func (m *TransportManager) RemoveForReload(tag string) error {
	return m.remove(tag, true)
}

func (m *TransportManager) remove(tag string, ignoreDependents bool) error {
	m.access.Lock()
	transport, found := m.transportByTag[tag]
	if !found {
		m.access.Unlock()
		return os.ErrInvalid
	}
	delete(m.transportByTag, tag)
	index := common.Index(m.transports, func(it adapter.DNSTransport) bool {
		return it == transport
	})
	if index == -1 {
		panic("invalid inbound index")
	}
	m.transports = slices.Delete(slices.Clone(m.transports), index, index+1) // H: never change a slice already handed out
	started := m.started
	if m.defaultTransport == transport {
		if len(m.transports) > 0 {
			nextTransport := m.transports[0]
			if nextTransport.Type() == C.DNSTypeFakeIP { // H: was inverted
				return E.New("default server cannot be fakeip")
			}
		}
		if len(dependBy) > 0 {
			m.access.Unlock()
			return E.New("server[", tag, "] is depended by ", strings.Join(dependBy, ", "))
		}
	}
	dependBy := m.dependByTag[tag]
	if len(dependBy) > 0 && !ignoreDependents {
		return E.New("server[", tag, "] is depended by ", strings.Join(dependBy, ", "))
	}
	m.removeDependencies(tag, transport.Dependencies())
	if m.fakeIPTransport != nil && adapter.DNSTransport(m.fakeIPTransport) == transport {
		m.fakeIPTransport = nil
	}
	if m.fakeIPTransport != nil && adapter.DNSTransport(m.fakeIPTransport) == transport {
		m.fakeIPTransport = nil
	}
	started := m.started
	m.access.Unlock()
	if started {
		return adapter.CloseLate(m.scope, transport)
	}
	return nil
}

func (m *TransportManager) Create(ctx context.Context, logger log.ContextLogger, tag string, transportType string, options any) error {
	if tag == "" {
		return os.ErrInvalid
	}
	transport, err := m.registry.CreateDNSTransport(ctx, logger, tag, transportType, options)
	if err != nil {
		return err
	}
	m.access.Lock()
	started := m.started
	m.access.Unlock()
	if started { // H: hot reload
		err = adapter.StartLate(m.scope, "dns/"+transport.Type()+"["+transport.Tag()+"]", transport)
		if err != nil {
			return err
		}
	}
	var replaced bool
	if existsTransport, loaded := m.transportByTag[tag]; loaded {
		if m.started {
			err = common.Close(existsTransport)
			if err != nil {
				return E.Cause(err, "close dns/", existsTransport.Type(), "[", existsTransport.Tag(), "]")
			}
		}
		existsIndex := common.Index(m.transports, func(it adapter.DNSTransport) bool {
			return it == existsTransport
		})
		if existsIndex == -1 {
			panic("invalid transport index")
		}
		// H: the replacement takes the old one's place (callers rely on the order, e.g. the main group
		// comes first); a copy, so a slice already handed out never changes
		m.transports = slices.Clone(m.transports)
		m.transports[existsIndex] = transport
		replaced = true
		// H: the replaced server no longer depends on anything, nor is it the fakeip server
		m.removeDependencies(tag, existsTransport.Dependencies())
		if m.fakeIPTransport != nil && adapter.DNSTransport(m.fakeIPTransport) == existsTransport {
			m.fakeIPTransport = nil
		}
	}
	if !replaced {
		m.transports = append(m.transports, transport)
	}
	m.transportByTag[tag] = transport
	err = m.setRoles(tag, transport, replaced && m.defaultTransport == existsTransport)
	m.access.Unlock()
	if replaced {
		closeErr := adapter.CloseLate(m.scope, existsTransport)
		if closeErr != nil {
			return E.Cause(closeErr, "close dns/", existsTransport.Type(), "[", existsTransport.Tag(), "]")
		}
	}
	return err
}

// setRoles makes a new server the default or the fakeip server where it should be. Call with access held.
func (m *TransportManager) setRoles(tag string, transport adapter.DNSTransport, replacesDefault bool) error {
	if tag == m.defaultTag || (m.defaultTag == "" && m.defaultTransport == nil) || replacesDefault {
		if transport.Type() == C.DNSTypeFakeIP {
			return E.New("default server cannot be fakeip")
		}
		m.defaultTransport = transport
	}
	if transport.Type() == C.DNSTypeFakeIP {
		if m.fakeIPTransport != nil {
			return E.New("multiple fakeip server are not supported")
		}
		m.fakeIPTransport = transport.(adapter.FakeIPTransport)
	}
	return nil
}

func (m *TransportManager) removeDependencies(tag string, dependencies []string) {
	for _, dependency := range dependencies {
		dependBy := common.Filter(m.dependByTag[dependency], func(it string) bool {
			return it != tag
		})
		if len(dependBy) == 0 {
			delete(m.dependByTag, dependency)
		} else {
			m.dependByTag[dependency] = dependBy
		}
	}
}

// SetDefaultTag changes the default server (dns.final); used by hot reload. //H
func (m *TransportManager) SetDefaultTag(tag string) error {
	m.access.Lock()
	defer m.access.Unlock()
	m.defaultTag = tag
	if tag == "" {
		return nil
	}
	transport, loaded := m.transportByTag[tag]
	if !loaded {
		return E.New("default DNS server not found: ", tag)
	}
	if transport.Type() == C.DNSTypeFakeIP {
		return E.New("default server cannot be fakeip")
	}
	if m.defaultTransport != transport {
		m.defaultTransport = transport
		m.logger.Info("updated default server to ", transport.Tag())
	}
	return nil
}
