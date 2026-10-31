package endpoint

import (
	"context"
	"os"
	"slices"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"
)

var _ adapter.EndpointManager = (*Manager)(nil)

type Manager struct {
	registry      adapter.EndpointRegistry
	access        sync.Mutex
	scope         *adapter.Scope
	started       bool // H: components created from now on are started right away (hot reload)
	endpoints     []adapter.Endpoint
	endpointByTag map[string]adapter.Endpoint
}

func NewManager(registry adapter.EndpointRegistry) *Manager {
	return &Manager{
		registry:      registry,
		endpointByTag: make(map[string]adapter.Endpoint),
	}
}

func (m *Manager) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	m.access.Lock()
	defer m.access.Unlock()
	if stage == adapter.StartStateInitialize {
		m.scope = scope
	}
	if stage == adapter.StartStateStart {
		return nil
	}
	for _, endpoint := range m.endpoints {
		name := "endpoint/" + endpoint.Type() + "[" + endpoint.Tag() + "]"
		err := scope.Start(name, endpoint, stage)
		if err != nil {
			return err
		}
	}
	if stage == adapter.StartStateStarted {
		m.started = true
	}
	return nil
}

func (m *Manager) StartEndpoint(endpoint adapter.Endpoint) error {
	return m.scope.Start("endpoint/"+endpoint.Type()+"["+endpoint.Tag()+"]", endpoint, adapter.StartStateStart)
}

func (m *Manager) Endpoints() []adapter.Endpoint {
	m.access.Lock()
	defer m.access.Unlock()
	return m.endpoints
}

func (m *Manager) Get(tag string) (adapter.Endpoint, bool) {
	m.access.Lock()
	defer m.access.Unlock()
	endpoint, found := m.endpointByTag[tag]
	return endpoint, found
}

// Remove removes and closes an endpoint; used by hot reload. //H
func (m *Manager) Remove(tag string) error {
	m.access.Lock()
	endpoint, found := m.endpointByTag[tag]
	if !found {
		m.access.Unlock()
		return os.ErrInvalid
	}
	delete(m.endpointByTag, tag)
	index := slices.Index(m.endpoints, endpoint)
	if index == -1 {
		panic("invalid endpoint index")
	}
	m.endpoints = slices.Delete(slices.Clone(m.endpoints), index, index+1) // H: never change a slice already handed out
	started := m.started
	m.access.Unlock()
	if started {
		return adapter.CloseLate(m.scope, endpoint)
	}
	return nil
}

func (m *Manager) Create(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, outboundType string, options any) error {
	endpoint, err := m.registry.Create(ctx, router, logger, tag, outboundType, options)
	if err != nil { return err }
	m.access.Lock()
	started, scope := m.started, m.scope
	m.access.Unlock()
	if started {
		if err = adapter.StartLate(scope, "endpoint/"+endpoint.Type()+"["+endpoint.Tag()+"]", endpoint); err != nil { return err }
	}
	m.access.Lock()
	old, replaced := m.endpointByTag[tag]
	if replaced {
		index := slices.Index(m.endpoints, old)
		if index < 0 { m.access.Unlock(); return E.New("invalid endpoint index: ", tag) }
		m.endpoints = slices.Clone(m.endpoints)
		m.endpoints[index] = endpoint
	} else {
		m.endpoints = append(slices.Clone(m.endpoints), endpoint)
	}
	m.endpointByTag[tag] = endpoint
	m.access.Unlock()
	if replaced {
		if err = adapter.CloseLate(scope, old); err != nil {
			return E.Cause(err, "close endpoint/", old.Type(), "[", old.Tag(), "]")
		}
	}
	return nil
}

}
