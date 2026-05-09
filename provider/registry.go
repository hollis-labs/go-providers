package provider

import (
	"sync"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
)

// Registry holds named Provider implementations. It is safe for concurrent use.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]llmcontracts.Provider
}

// NewRegistry creates a new empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]llmcontracts.Provider),
	}
}

// Register adds a provider under the given name.
func (r *Registry) Register(name string, p llmcontracts.Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[name] = p
}

// Unregister removes the provider registered under name, returning true if it was present.
func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.providers[name]; !ok {
		return false
	}
	delete(r.providers, name)
	return true
}

// Get returns the provider for the given name, or false if not found.
func (r *Registry) Get(name string) (llmcontracts.Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	return p, ok
}

// Has returns true if a provider is registered under the given name.
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.providers[name]
	return ok
}

// Names returns all registered provider names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}
