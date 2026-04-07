package provider

// Registry holds named Provider implementations.
type Registry struct {
	providers map[string]Provider
}

// NewRegistry creates a new empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
	}
}

// Register adds a provider under the given name.
func (r *Registry) Register(name string, p Provider) {
	r.providers[name] = p
}

// Get returns the provider for the given name, or false if not found.
func (r *Registry) Get(name string) (Provider, bool) {
	p, ok := r.providers[name]
	return p, ok
}

// Has returns true if a provider is registered under the given name.
func (r *Registry) Has(name string) bool {
	_, ok := r.providers[name]
	return ok
}

// Names returns all registered provider names.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}
