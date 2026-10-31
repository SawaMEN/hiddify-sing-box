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
	if !ignoreDependents {
		var dependBy []string
		for _, candidate := range m.transports {
			if candidate.Tag() != tag && slices.Contains(candidate.Dependencies(), tag) {
				dependBy = append(dependBy, candidate.Tag())
			}
		}
		if len(dependBy) != 0 {
			m.access.Unlock()
			return E.New("server[", tag, "] is depended by ", strings.Join(dependBy, ", "))
		}
	}
	index := slices.Index(m.transports, transport)
	if index < 0 {
		m.access.Unlock()
		return E.New("invalid DNS transport index: ", tag)
	}
	m.transports = slices.Delete(slices.Clone(m.transports), index, index+1)
	delete(m.transportByTag, tag)
	if m.fakeIPTransport != nil && adapter.DNSTransport(m.fakeIPTransport) == transport {
		m.fakeIPTransport = nil
	}
	if m.defaultTransport == transport {
		m.defaultTransport = nil
		for _, candidate := range m.transports {
			if candidate.Type() != C.DNSTypeFakeIP {
				m.defaultTransport = candidate
				break
			}
		}
		if m.defaultTag == tag {
			m.defaultTag = ""
		}
	}
	started, scope := m.started, m.scope
	m.access.Unlock()
	if started {
		return adapter.CloseLate(scope, transport)
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
	m.access.RLock()
	started, scope := m.started, m.scope
	m.access.RUnlock()
	if started {
		if err = adapter.StartLate(scope, "dns/"+transport.Type()+"["+transport.Tag()+"]", transport); err != nil {
			return err
		}
	}
	m.access.Lock()
	old, replaced := m.transportByTag[tag]
	becomesDefault := tag == m.defaultTag || (m.defaultTag == "" && m.defaultTransport == nil) || (replaced && m.defaultTransport == old)
	if becomesDefault && transport.Type() == C.DNSTypeFakeIP {
		m.access.Unlock()
		return E.New("default server cannot be fakeip")
	}
	if transport.Type() == C.DNSTypeFakeIP && m.fakeIPTransport != nil && (!replaced || adapter.DNSTransport(m.fakeIPTransport) != old) {
		m.access.Unlock()
		return E.New("multiple fakeip servers are not supported")
	}
	if replaced {
		index := slices.Index(m.transports, old)
		if index < 0 {
			m.access.Unlock()
			return E.New("invalid DNS transport index: ", tag)
		}
		m.transports = slices.Clone(m.transports)
		m.transports[index] = transport
		if m.fakeIPTransport != nil && adapter.DNSTransport(m.fakeIPTransport) == old {
			m.fakeIPTransport = nil
		}
	} else {
		m.transports = append(slices.Clone(m.transports), transport)
	}
	m.transportByTag[tag] = transport
	err = m.setRoles(tag, transport, replaced && m.defaultTransport == old)
	m.access.Unlock()
	if replaced {
		if closeErr := adapter.CloseLate(scope, old); closeErr != nil {
			return E.Cause(closeErr, "close dns/", old.Type(), "[", old.Tag(), "]")
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
	}
	return nil
}
