package provider

import (
	"context"
	"fmt"
)

type exampleProvider struct{}

func (exampleProvider) StreamChat(ctx context.Context, in ChatRequest) (<-chan StreamEvent, error) {
	return nil, fmt.Errorf("not implemented")
}

func (exampleProvider) Complete(ctx context.Context, in ChatRequest) (string, error) {
	return "", nil
}

func (exampleProvider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{}
}

func ExampleRegistry() {
	reg := NewRegistry()
	reg.Register("demo", exampleProvider{})

	_, ok := reg.Get("demo")
	fmt.Println(ok)
	// Output:
	// true
}
