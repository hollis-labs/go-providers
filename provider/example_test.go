package provider

import (
	"context"
	"fmt"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

type exampleProvider struct{}

func (exampleProvider) StreamChat(ctx context.Context, in llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	return nil, fmt.Errorf("not implemented")
}

func (exampleProvider) Complete(ctx context.Context, in llmtypes.ChatRequest) (string, error) {
	return "", nil
}

func (exampleProvider) Capabilities() llmtypes.ProviderCapabilities {
	return llmtypes.ProviderCapabilities{}
}

func ExampleRegistry() {
	reg := NewRegistry()
	reg.Register("demo", exampleProvider{})

	_, ok := reg.Get("demo")
	fmt.Println(ok)
	// Output:
	// true
}
