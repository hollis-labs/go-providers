package provider

import (
	"context"
	"sync"
	"testing"
)

// stubProvider is a minimal Provider used for registry tests.
type stubProvider struct{}

func (stubProvider) StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (<-chan StreamEvent, error) {
	return nil, nil
}
func (stubProvider) StreamChatWithTools(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	return nil, nil
}
func (stubProvider) Complete(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (string, error) {
	return "", nil
}
func (stubProvider) Capabilities() ProviderCapabilities { return ProviderCapabilities{} }

func TestRegistryUnregisterRemoves(t *testing.T) {
	r := NewRegistry()
	r.Register("foo", stubProvider{})
	if !r.Has("foo") {
		t.Fatal("expected provider registered")
	}
	if !r.Unregister("foo") {
		t.Fatal("Unregister should return true for present name")
	}
	if r.Has("foo") {
		t.Fatal("provider should be gone after Unregister")
	}
	if _, ok := r.Get("foo"); ok {
		t.Fatal("Get should miss after Unregister")
	}
}

func TestRegistryUnregisterAbsent(t *testing.T) {
	r := NewRegistry()
	if r.Unregister("missing") {
		t.Fatal("Unregister should return false for absent name")
	}
}

func TestRegistryConcurrent(t *testing.T) {
	r := NewRegistry()
	const goroutines = 10
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines * 3)

	for i := 0; i < goroutines; i++ {
		name := "p"
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				r.Register(name, stubProvider{})
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				r.Unregister(name)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_, _ = r.Get(name)
				_ = r.Has(name)
				_ = r.Names()
			}
		}()
	}
	wg.Wait()
}
