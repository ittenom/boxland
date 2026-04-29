package editor

import (
	"sync"
)

// Manager is the global registry of live editor sessions. The WS
// gateway uses it to look up or create the Session for a
// (kind, target_id) pair when a designer joins.
//
// Lifecycle: a session is born on the first Subscribe and lives
// until the last subscriber unsubscribes. The manager doesn't
// destroy sessions on idle (rare; tests can call CloseSession to
// force tear-down). Cross-tab co-edit is the canonical case where
// keeping the session alive across short subscriber gaps matters.
type Manager struct {
	mu       sync.RWMutex
	sessions map[SessionKey]*Session
}

// NewManager allocates an empty registry.
func NewManager() *Manager {
	return &Manager{sessions: make(map[SessionKey]*Session)}
}

// GetOrCreate returns the session for `key`, creating one if
// none exists. Concurrent callers see the same Session instance.
func (m *Manager) GetOrCreate(key SessionKey) *Session {
	m.mu.RLock()
	if s, ok := m.sessions[key]; ok {
		m.mu.RUnlock()
		return s
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[key]; ok {
		return s
	}
	s := NewSession(key)
	m.sessions[key] = s
	return s
}

// Find returns the session for `key`, or nil. Used by tests +
// admin tooling that wants to peek without spawning sessions.
func (m *Manager) Find(key SessionKey) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[key]
}

// CloseSession tears the session down + drops it from the
// registry. Idempotent.
func (m *Manager) CloseSession(key SessionKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[key]; ok {
		s.Close()
		delete(m.sessions, key)
	}
}

// Sessions returns a snapshot of every live key. Useful for
// admin dashboards / debug logging. Order is not stable.
func (m *Manager) Sessions() []SessionKey {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]SessionKey, 0, len(m.sessions))
	for k := range m.sessions {
		out = append(out, k)
	}
	return out
}
