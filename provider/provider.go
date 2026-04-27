package provider

import (
	"context"
	"os"
	"strings"
)

// ProviderCapabilities describes the capabilities supported by a provider.
type ProviderCapabilities struct {
	// SupportsStreamJSON indicates if the provider supports streaming responses with JSON tools
	SupportsStreamJSON bool
	// SupportsPreToolHooks indicates if the provider supports pre-tool execution hooks
	SupportsPreToolHooks bool
	// SupportsPostToolHooks indicates if the provider supports post-tool execution hooks
	SupportsPostToolHooks bool
	// SupportsSystemPromptCaching indicates if the provider supports system prompt caching
	SupportsSystemPromptCaching bool
	// SupportsToolCalling indicates if the provider supports tool/function calling
	SupportsToolCalling bool
	// SupportsBatch indicates if the provider supports batch processing
	SupportsBatch bool
	// SupportsImageInput indicates if the provider supports image inputs
	SupportsImageInput bool
	// MaxTokens is the maximum *output* tokens the model can generate in a single response
	// (e.g., 16384 for Claude Sonnet). Do NOT set this to the context window size.
	// 0 means no limit specified (provider default applies).
	MaxTokens int
	// ContextWindowSize is the total context window in tokens (input + output combined,
	// e.g., 200000 for Claude). Used by slot-based context assembly for budget computation.
	// 0 means unknown (falls back to default).
	ContextWindowSize int
	// SupportsEmbedding indicates if the provider supports text embedding.
	SupportsEmbedding bool
	// DefaultEmbeddingModel is the default model name for embedding, if supported.
	DefaultEmbeddingModel string
}

// ToolDefinition describes a tool available to the LLM.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
	// Strict controls whether the provider enforces strict schema validation on
	// tool inputs. When nil (the default), strict mode is enabled. Set to a
	// pointer to false to opt out on a per-tool basis.
	//
	// Strict mode causes Anthropic to guarantee that tool call inputs conform to
	// the declared InputSchema, catching malformed calls early instead of
	// wasting retry turns.
	//
	// Not all providers honour this flag (e.g. OpenAI tool calling is not
	// implemented). Providers that do not support strict mode silently ignore it.
	Strict *bool `json:"strict,omitempty"`
}

// ToolUseBlock represents a tool_use content block from the LLM.
type ToolUseBlock struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// ContentBlock represents a content block in a multi-block message.
// NOTE: Input uses a pointer to distinguish "absent" from "empty object".
// Anthropic requires the input field on tool_use blocks even when empty.
type ContentBlock struct {
	Type      string          `json:"type"`                  // text, tool_use, tool_result
	Text      string          `json:"text,omitempty"`        // text block
	ID        string          `json:"id,omitempty"`          // tool_use block ID
	Name      string          `json:"name,omitempty"`        // tool_use tool name
	Input     *map[string]any `json:"input,omitempty"`       // tool_use input (always set for tool_use blocks)
	ToolUseID string          `json:"tool_use_id,omitempty"` // tool_result reference
	Content   string          `json:"content,omitempty"`     // tool_result text
	IsError   bool            `json:"is_error,omitempty"`    // tool_result error flag (Anthropic API)
}

// StreamEvent represents a single event from a streaming provider response.
type StreamEvent struct {
	Type      string        // "delta", "tool_use", "usage", "error", "done", "session_id"
	Content   string        // text delta
	Usage     *Usage        // only on "usage" or "done" events
	Error     string        // only on "error" events
	ToolUse   *ToolUseBlock // only on "tool_use" events
	SessionID string        // only on "session_id" events (PTY bridge: CLI session ID for --resume)
}

// Usage contains token usage information.
type Usage struct {
	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int
	StopReason          string
}

// CompleteResult contains the text and optional usage metadata for a
// non-streaming completion call.
type CompleteResult struct {
	Text  string
	Usage *Usage
}

// ChatMessage represents a single message in a conversation.
// For simple text messages, use the Content field.
// For multi-block messages (e.g. tool_result), use ContentBlocks.
type ChatMessage struct {
	Role          string         `json:"role"`
	Content       string         `json:"content,omitempty"`
	ContentBlocks []ContentBlock `json:"content_blocks,omitempty"`
}

// SlotBlock is a pre-assembled region of the context window. Providers that
// support prompt caching (e.g., Anthropic) translate each block into a
// cache-aware content block; providers that don't concatenate them into the
// system prompt.
//
// Changed == false means the block's content matches the previous turn's
// cache key; adapters may use this to emit cache markers.
type SlotBlock struct {
	// Name is the slot identifier (e.g., "system", "memory", "conversation").
	Name string
	// Content is the rendered text for this slot.
	Content string
	// CacheKey is a content hash used by callers to detect changes across turns.
	CacheKey string
	// Changed is true when CacheKey differs from the previous turn.
	Changed bool
}

// ChatRequest is the unified input to provider chat methods. Tools are
// optional; providers that don't support tools ignore the field. SlotBlocks
// are optional; when non-empty they extend SystemPrompt (appended after it)
// and give adapters the chance to emit slot-aware payloads (e.g., Anthropic
// cache_control). To avoid duplication, callers should put system content
// exclusively in SlotBlocks and leave SystemPrompt empty.
type ChatRequest struct {
	Model        string
	SystemPrompt string
	SlotBlocks   []SlotBlock
	Messages     []ChatMessage
	Tools        []ToolDefinition
}

// EffectiveSystemPrompt returns SystemPrompt when no slots are set, otherwise
// returns SystemPrompt (if non-empty) followed by each non-empty slot's content
// joined with "\n\n". Adapters that don't exploit slot boundaries should call
// this to preserve the full semantic content.
func (r ChatRequest) EffectiveSystemPrompt() string {
	if len(r.SlotBlocks) == 0 {
		return r.SystemPrompt
	}
	var b strings.Builder
	if r.SystemPrompt != "" {
		b.WriteString(r.SystemPrompt)
	}
	for _, s := range r.SlotBlocks {
		if s.Content == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(s.Content)
	}
	return b.String()
}

// Provider is the interface for LLM provider adapters.
type Provider interface {
	// StreamChat streams a response for the given request. Tools are optional.
	StreamChat(ctx context.Context, req ChatRequest) (<-chan StreamEvent, error)
	// Complete makes a non-streaming completion call.
	Complete(ctx context.Context, req ChatRequest) (string, error)
	// Capabilities returns the capabilities supported by this provider.
	Capabilities() ProviderCapabilities
}

// ProviderWithUsage is an optional extension interface for providers that can
// return token usage for non-streaming completions.
type ProviderWithUsage interface {
	Provider
	// CompleteWithUsage makes a non-streaming completion call and returns
	// any token usage the underlying provider surfaces.
	CompleteWithUsage(ctx context.Context, req ChatRequest) (CompleteResult, error)
}

// ptySessionKeyType is the context key for passing a CLI session ID
// into the PTY bridge for --resume support.
type ptySessionKeyType struct{}

// WithCLISessionID returns a context carrying the given CLI session ID.
// The PTY bridge reads this to decide whether to use --resume.
func WithCLISessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ptySessionKeyType{}, id)
}

// CLISessionIDFromContext extracts the CLI session ID from the context, if set.
func CLISessionIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(ptySessionKeyType{}).(string)
	return id, ok && id != ""
}

// sandboxDirKeyType is the context key for passing a sandbox directory
// path into the PTY bridge.
type sandboxDirKeyType struct{}

// WithSandboxDir returns a context carrying the given sandbox directory path.
// The PTY bridge reads this to set cmd.Dir.
func WithSandboxDir(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, sandboxDirKeyType{}, dir)
}

// SandboxDirFromContext extracts the sandbox directory from the context, if set.
func SandboxDirFromContext(ctx context.Context) (string, bool) {
	dir, ok := ctx.Value(sandboxDirKeyType{}).(string)
	return dir, ok && dir != ""
}

// ProcessCallback is called by PTY/subprocess bridges after spawning a CLI process
// and again when the process exits. This enables external process tracking without
// the provider package importing the chat package.
type ProcessCallback func(proc *os.Process, started bool)

type processCallbackKeyType struct{}

// WithProcessCallback returns a context carrying a process lifecycle callback.
func WithProcessCallback(ctx context.Context, cb ProcessCallback) context.Context {
	return context.WithValue(ctx, processCallbackKeyType{}, cb)
}

// ProcessCallbackFromContext extracts the process callback from the context, if set.
func ProcessCallbackFromContext(ctx context.Context) (ProcessCallback, bool) {
	cb, ok := ctx.Value(processCallbackKeyType{}).(ProcessCallback)
	return cb, ok && cb != nil
}

// ActivityCallback is called by PTY/subprocess bridges when output is received
// from a CLI process. Used by the process tracker to detect hung processes.
type ActivityCallback func(pid int)

type activityCallbackKeyType struct{}

// WithActivityCallback returns a context carrying an activity callback.
func WithActivityCallback(ctx context.Context, cb ActivityCallback) context.Context {
	return context.WithValue(ctx, activityCallbackKeyType{}, cb)
}

// ActivityCallbackFromContext extracts the activity callback from the context, if set.
func ActivityCallbackFromContext(ctx context.Context) (ActivityCallback, bool) {
	cb, ok := ctx.Value(activityCallbackKeyType{}).(ActivityCallback)
	return cb, ok && cb != nil
}
