package outbound

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
)

var _ adapter.OutboundManager = (*Manager)(nil)

type Manager struct {
	registry                adapter.OutboundRegistry
	endpoint                adapter.EndpointManager
	defaultTag              string
	access                  sync.RWMutex
	outbounds               []adapter.Outbound
	outboundByTag           map[string]adapter.Outbound
	defaultOutbound         adapter.Outbound
	defaultOutboundFallback func() (adapter.Outbound, error)
	scope                   *adapter.Scope // H: where outbounds created by hot reload are started
	started                 bool
}

func NewManager(registry adapter.OutboundRegistry, endpoint adapter.EndpointManager, defaultTag string) *Manager {
	return &Manager{
		registry:      registry,
		endpoint:      endpoint,
		defaultTag:    defaultTag,
		outboundByTag: make(map[string]adapter.Outbound),
	}
}

func (m *Manager) Initialize(defaultOutboundFallback func() (adapter.Outbound, error)) {
	m.defaultOutboundFallback = defaultOutboundFallback
}

func (m *Manager) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	m.access.Lock()
	m.scope = scope
	if stage == adapter.StartStateInitialize {
		if m.defaultTag != "" && m.defaultOutbound == nil {
			defaultEndpoint, loaded := m.endpoint.Get(m.defaultTag)
			if !loaded {
				m.access.Unlock()
				return E.New("default outbound not found: ", m.defaultTag)
			}
			m.defaultOutbound = defaultEndpoint
		}
		if m.defaultOutbound == nil {
			directOutbound, err := m.defaultOutboundFallback()
			if err != nil {
				m.access.Unlock()
				return E.Cause(err, "create direct outbound for fallback")
			}
			m.outbounds = append(m.outbounds, directOutbound)
			m.outboundByTag[directOutbound.Tag()] = directOutbound
			m.defaultOutbound = directOutbound
		}
	}
	outbounds := m.outbounds
	m.access.Unlock()
	if stage == adapter.StartStateStart {
		return m.startOutbounds(scope, append(outbounds, common.Map(m.endpoint.Endpoints(), func(it adapter.Endpoint) adapter.Outbound { return it })...))
	}
	for _, outbound := range outbounds {
		lifecycle, isLifecycle := adapter.AsLifecycle(outbound) // H: also closes outbounds that only have Close
		if !isLifecycle {
			continue
		}
		name := "outbound/" + outbound.Type() + "[" + outbound.Tag() + "]"
		err := scope.Start(name, lifecycle, stage)
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

func (m *Manager) startOutbounds(scope *adapter.Scope, outbounds []adapter.Outbound) error {
	started := make(map[string]bool)
	for {
		canContinue := false
	startOne:
		for _, outboundToStart := range outbounds {
			outboundTag := outboundToStart.Tag()
			if started[outboundTag] {
				continue
			}
			dependencies := outboundToStart.Dependencies()
			for _, dependency := range dependencies {
				if !started[dependency] {
					continue startOne
				}
			}
			started[outboundTag] = true
			canContinue = true
			if endpoint, isEndpoint := outboundToStart.(adapter.Endpoint); isEndpoint {
				err := m.endpoint.StartEndpoint(endpoint)
				if err != nil {
					return err
				}
				continue
			}
			lifecycle, isLifecycle := adapter.AsLifecycle(outboundToStart) // H
			if !isLifecycle {
				continue
			}
			name := "outbound/" + outboundToStart.Type() + "[" + outboundTag + "]"
			err := scope.Start(name, lifecycle, adapter.StartStateStart)
			if err != nil {
				return err
			}
		}
		if len(started) == len(outbounds) {
			break
		}
		if canContinue {
			continue
		}
		currentOutbound := common.Find(outbounds, func(it adapter.Outbound) bool {
			return !started[it.Tag()]
		})
		var lintOutbound func(oTree []string, oCurrent adapter.Outbound) error
		lintOutbound = func(oTree []string, oCurrent adapter.Outbound) error {
			problemOutboundTag := common.Find(oCurrent.Dependencies(), func(it string) bool {
				return !started[it]
			})
			if common.Contains(oTree, problemOutboundTag) {
				return E.New("circular outbound dependency: ", strings.Join(oTree, " -> "), " -> ", problemOutboundTag)
			}
			problemOutbound := common.Find(outbounds, func(it adapter.Outbound) bool {
				return it.Tag() == problemOutboundTag
			})
			if problemOutbound == nil {
				return E.New("dependency[", problemOutboundTag, "] not found for outbound[", oCurrent.Tag(), "]")
			}
			return lintOutbound(append(oTree, problemOutboundTag), problemOutbound)
		}
		return lintOutbound([]string{currentOutbound.Tag()}, currentOutbound)
	}
	return nil
}

func (m *Manager) Outbounds() []adapter.Outbound {
	m.access.RLock()
	defer m.access.RUnlock()
	return m.outbounds
}

func (m *Manager) Outbound(tag string) (adapter.Outbound, bool) {
	m.access.RLock()
	outbound, found := m.outboundByTag[tag]
	m.access.RUnlock()
	if found {
		return outbound, true
	}
	return m.endpoint.Get(tag)
}

func (m *Manager) Default() adapter.Outbound {
	m.access.RLock()
	defer m.access.RUnlock()
	return m.defaultOutbound
}

func (m *Manager) Remove(tag string) error {
	return m.remove(tag, false)
}

// RemoveForReload removes an outbound even while others still depend on it; used by hot reload,
// which recreates those dependents in the same reload. //H
func (m *Manager) RemoveForReload(tag string) error {
	return m.remove(tag, true)
}

func (m *Manager) remove(tag string, ignoreDependents bool) error {
	m.access.Lock()
	outbound, found := m.outboundByTag[tag]
	if !found { m.access.Unlock(); return os.ErrInvalid }
	if !ignoreDependents {
		var dependBy []string
		for _, candidate := range m.outbounds {
			if candidate.Tag() != tag && slices.Contains(candidate.Dependencies(), tag) {
				dependBy = append(dependBy, candidate.Tag())
			}
		}
		if len(dependBy) != 0 {
			m.access.Unlock()
			return E.New("outbound[", tag, "] is depended by ", strings.Join(dependBy, ", "))
		}
	}
	index := slices.Index(m.outbounds, outbound)
	if index < 0 { m.access.Unlock(); return E.New("invalid outbound index: ", tag) }
	m.outbounds = slices.Delete(slices.Clone(m.outbounds), index, index+1)
	delete(m.outboundByTag, tag)
	if m.defaultOutbound == outbound {
		m.defaultOutbound = nil
		if len(m.outbounds) > 0 { m.defaultOutbound = m.outbounds[0] }
	}
	started, scope := m.started, m.scope
	m.access.Unlock()
	if started { return adapter.CloseLate(scope, outbound) }
	return nil
}

func (m *Manager) Create(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, inboundType string, options any) error {
	if tag == "" { return os.ErrInvalid }
	option.MigrateTLSFragment(options) //H
	outbound, err := m.registry.CreateOutbound(ctx, router, logger, tag, inboundType, options)
	if err != nil { return err }
	m.access.RLock()
	started, scope := m.started, m.scope
	m.access.RUnlock()
	if started {
		if err = adapter.StartLate(scope, "outbound/"+outbound.Type()+"["+outbound.Tag()+"]", outbound); err != nil { return err }
	}
	m.access.Lock()
	old, replaced := m.outboundByTag[tag]
	if replaced {
		index := slices.Index(m.outbounds, old)
		if index < 0 { m.access.Unlock(); return E.New("invalid outbound index: ", tag) }
		m.outbounds = slices.Clone(m.outbounds)
		m.outbounds[index] = outbound
	} else {
		m.outbounds = append(slices.Clone(m.outbounds), outbound)
	}
	m.outboundByTag[tag] = outbound
	if tag == m.defaultTag || (m.defaultTag == "" && m.defaultOutbound == nil) || (replaced && m.defaultOutbound == old) {
		m.defaultOutbound = outbound
	}
	m.access.Unlock()
	if replaced {
		if err = adapter.CloseLate(scope, old); err != nil { return E.Cause(err, "close outbound/", old.Type(), "[", old.Tag(), "]") }
	}
	return nil
}

// SetDefaultTag changes the default outbound (route.final); used by hot reload. //H
func (m *Manager) SetDefaultTag(tag string) error {
	m.access.Lock()
	m.defaultTag = tag
	if tag == "" {
		m.access.Unlock()
		return nil
	}
	outbound, loaded := m.outboundByTag[tag]
	m.access.Unlock()
	if !loaded {
		endpoint, endpointLoaded := m.endpoint.Get(tag)
		if !endpointLoaded {
			return E.New("default outbound not found: ", tag)
		}
		outbound = endpoint
	}
	m.access.Lock()
	defer m.access.Unlock()
	if m.defaultOutbound != outbound {
		m.defaultOutbound = outbound
	}
	return nil
}
