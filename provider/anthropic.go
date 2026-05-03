package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/go-providers/internal/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const anthropicAPI = "https://api.anthropic.com/v1/messages"

// anthropicBetaBase is the baseline beta header value sent on all Anthropic
// streaming requests (prompt caching).
const anthropicBetaBase = "prompt-caching-2024-07-31"

// InterleavedThinkingBetaHeader is the beta header value that enables
// interleaved thinking (thinking_delta blocks). Supported on Claude Opus/Sonnet/
// Haiku 4.x models with a release-date suffix on or after 20250514.
// F3 / CW-20260420-0023.
const InterleavedThinkingBetaHeader = "interleaved-thinking-2025-05-14"

// minInterleavedThinkingModelDate is the earliest YYYYMMDD release-date suffix
// for which interleaved thinking is supported (2025-05-14 GA).
const minInterleavedThinkingModelDate = 20250514

// modelSupportsInterleavedThinking reports whether the given model ID supports
// the interleaved-thinking-2025-05-14 beta feature. Accepts the canonical
// Anthropic naming pattern claude-{opus|sonnet|haiku}-4[-<minor>]-<YYYYMMDD>
// and requires the trailing date to be on or after minInterleavedThinkingModelDate.
//
// Examples accepted:
//
//	claude-opus-4-20250514
//	claude-sonnet-4-5-20250930
//	claude-haiku-4-5-20251001
//
// Examples rejected: anything outside the family, claude-{family}-4 with date
// before 20250514, or false-prefix matches like claude-opus-40-*.
func modelSupportsInterleavedThinking(model string) bool {
	parts := strings.Split(strings.ToLower(model), "-")
	// Accept either claude-{family}-4-{date} (4 parts) or
	// claude-{family}-4-{minor}-{date} (5 parts).
	if len(parts) != 4 && len(parts) != 5 {
		return false
	}
	if parts[0] != "claude" {
		return false
	}
	switch parts[1] {
	case "opus", "sonnet", "haiku":
	default:
		return false
	}
	// The major-version segment must be exactly "4" (rejects e.g. "40").
	if parts[2] != "4" {
		return false
	}
	if len(parts) == 5 && !allDigits(parts[3]) {
		return false
	}
	datePart := parts[len(parts)-1]
	if len(datePart) != 8 || !allDigits(datePart) {
		return false
	}
	dateValue, err := strconv.Atoi(datePart)
	if err != nil {
		return false
	}
	return dateValue >= minInterleavedThinkingModelDate
}

// allDigits reports whether s is non-empty and contains only ASCII digits.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// shouldEnableInterleavedThinking decides whether to send the
// interleaved-thinking beta header and the thinking_config request parameter
// for a request. The two are sent as a pair: Anthropic ignores the beta header
// without a corresponding thinking_config, so we gate them together to avoid
// silent no-ops.
func shouldEnableInterleavedThinking(cfg ReasoningConfig, model string) bool {
	return cfg.Enabled &&
		cfg.BudgetTokens > 0 &&
		cfg.BetasHeader == InterleavedThinkingBetaHeader &&
		modelSupportsInterleavedThinking(model)
}

// Anthropic implements the Provider and CacheableProvider interfaces for the Anthropic Messages API.
type Anthropic struct {
	apiKey         string
	client         *http.Client
	Retry          RetryConfig
	OnStatus       StatusCallback // optional; called during retries to report status
	CircuitBreaker *CircuitBreaker
	OnCircuitOpen  func() // called when the circuit breaker trips
	RateTracker    *TokenRateTracker
	cacheHints     []CacheHint // set via SetCacheHints before each request
}

// NewAnthropic creates a new Anthropic provider. It reads ANTHROPIC_API_KEY from the environment.
func NewAnthropic() *Anthropic {
	return &Anthropic{
		apiKey:         "",
		client:         &http.Client{},
		Retry:          DefaultRetryConfig(),
		CircuitBreaker: NewCircuitBreaker(3),
		RateTracker:    NewTokenRateTracker(50000),
	}
}

// SetCacheHints implements CacheableProvider. It stores the hints so that
// subsequent calls to buildSystemBlocks, buildToolsWithCacheControl, and
// marshalMessages apply cache_control markers accordingly.
func (a *Anthropic) SetCacheHints(hints []CacheHint) {
	a.cacheHints = hints
}

// hasCacheHint checks whether the stored hints include one matching the given position.
func (a *Anthropic) hasCacheHint(position string) bool {
	for _, h := range a.cacheHints {
		if h.Position == position {
			return true
		}
	}
	return false
}

// recentMessageCacheCount returns the number of "recent_message" hints,
// which controls how many trailing user messages get cache_control markers.
func (a *Anthropic) recentMessageCacheCount() int {
	count := 0
	for _, h := range a.cacheHints {
		if h.Position == "recent_message" {
			count++
		}
	}
	return count
}

// anthropicRequest is the request body for the Anthropic Messages API.
type anthropicRequest struct {
	Model         string `json:"model"`
	MaxTokens     int    `json:"max_tokens"`
	System        any    `json:"system,omitempty"`
	Messages      []any  `json:"messages"`
	Stream        bool   `json:"stream"`
	Tools         []any  `json:"tools,omitempty"`
	// ThinkingConfig enables extended thinking when set. Only sent when
	// interleaved thinking is active (F3 / CW-20260420-0023).
	ThinkingConfig *anthropicThinkingConfig `json:"thinking,omitempty"`
}

// anthropicThinkingConfig is the thinking_config parameter for the
// Anthropic Messages API when extended/interleaved thinking is enabled.
type anthropicThinkingConfig struct {
	Type      string `json:"type"`       // "enabled"
	BudgetTokens int `json:"budget_tokens"`
}

// buildSystemBlocks wraps a system prompt in a content block.
// If the provider has a "system" cache hint, the block gets cache_control ephemeral.
func (a *Anthropic) buildSystemBlocks(systemPrompt string) []map[string]any {
	if systemPrompt == "" {
		return nil
	}
	block := map[string]any{
		"type": "text",
		"text": systemPrompt,
	}
	if a.hasCacheHint("system") {
		block["cache_control"] = map[string]string{"type": "ephemeral"}
	}
	return []map[string]any{block}
}

// buildSystemFromRequest renders the system portion of a request. When the
// request carries SlotBlocks, each block becomes its own content block and
// unchanged blocks (Changed == false) get a cache_control: ephemeral marker.
// When there are no slots, falls back to the legacy flat-system path.
func (a *Anthropic) buildSystemFromRequest(in ChatRequest) []map[string]any {
	if len(in.SlotBlocks) == 0 {
		return a.buildSystemBlocks(in.SystemPrompt)
	}
	out := make([]map[string]any, 0, len(in.SlotBlocks)+1)
	if in.SystemPrompt != "" {
		block := map[string]any{"type": "text", "text": in.SystemPrompt}
		if a.hasCacheHint("system") {
			block["cache_control"] = map[string]string{"type": "ephemeral"}
		}
		out = append(out, block)
	}
	for _, s := range in.SlotBlocks {
		if s.Content == "" {
			continue
		}
		block := map[string]any{"type": "text", "text": s.Content}
		if !s.Changed {
			block["cache_control"] = map[string]string{"type": "ephemeral"}
		}
		out = append(out, block)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// buildSystemBlocks is the package-level (static) version used by tests and
// non-method callers. It always applies cache_control for backwards compatibility.
func buildSystemBlocks(systemPrompt string) []map[string]any {
	if systemPrompt == "" {
		return nil
	}
	return []map[string]any{
		{
			"type": "text",
			"text": systemPrompt,
			"cache_control": map[string]string{
				"type": "ephemeral",
			},
		},
	}
}

// buildToolsWithCacheControl converts tool definitions to []any.
// If the provider has a "tools" cache hint, the last tool gets cache_control ephemeral.
// Strict is emitted as strict: true only when the caller explicitly sets Strict to a pointer to true.
// Default (nil) is non-strict — handler-level validation is preferred over Anthropic's server-side
// input-schema enforcement.
func (a *Anthropic) buildToolsWithCacheControl(tools []ToolDefinition) []any {
	if len(tools) == 0 {
		return nil
	}
	shouldCache := a.hasCacheHint("tools")
	result := make([]any, len(tools))
	for i, t := range tools {
		entry := map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.InputSchema,
		}
		if t.Strict != nil && *t.Strict {
			entry["strict"] = true
		}
		if shouldCache && i == len(tools)-1 {
			entry["cache_control"] = map[string]string{"type": "ephemeral"}
		}
		result[i] = entry
	}
	return result
}

// buildToolsWithCacheControl is the package-level (static) version for tests.
// It always marks the last tool with cache_control.
// Strict is emitted as strict: true only when the caller explicitly sets Strict to a pointer to true.
// Default (nil) is non-strict — handler-level validation is preferred over Anthropic's server-side
// input-schema enforcement.
func buildToolsWithCacheControl(tools []ToolDefinition) []any {
	if len(tools) == 0 {
		return nil
	}
	result := make([]any, len(tools))
	for i, t := range tools {
		entry := map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.InputSchema,
		}
		if t.Strict != nil && *t.Strict {
			entry["strict"] = true
		}
		if i == len(tools)-1 {
			entry["cache_control"] = map[string]string{"type": "ephemeral"}
		}
		result[i] = entry
	}
	return result
}

// marshalMessages converts ChatMessage slice to the Anthropic API format.
// The number of trailing user messages that receive cache_control is driven
// by the "recent_message" cache hints.
func (a *Anthropic) marshalMessages(messages []ChatMessage) []any {
	cacheCount := a.recentMessageCacheCount()
	return marshalMessagesWithCacheCount(messages, cacheCount)
}

// marshalMessages is the legacy static version used by existing tests.
// It always caches the last 2 user messages for backwards compatibility.
func marshalMessages(messages []ChatMessage) []any {
	return marshalMessagesWithCacheCount(messages, 2)
}

// contentBlockToMap converts a ContentBlock to the Anthropic API map format.
// Thinking blocks (type="thinking") require "thinking" and "signature" keys
// rather than "text" — this function handles that translation.
// F3 / CW-20260420-0023.
func contentBlockToMap(b ContentBlock) map[string]any {
	block := map[string]any{"type": b.Type}
	// thinking blocks use "thinking" key instead of "text"
	if b.Type == "thinking" {
		if b.Text != "" {
			block["thinking"] = b.Text
		}
		if b.Signature != "" {
			block["signature"] = b.Signature
		}
		return block
	}
	if b.Text != "" {
		block["text"] = b.Text
	}
	if b.ID != "" {
		block["id"] = b.ID
	}
	if b.Name != "" {
		block["name"] = b.Name
	}
	if b.Input != nil {
		block["input"] = b.Input
	}
	if b.ToolUseID != "" {
		block["tool_use_id"] = b.ToolUseID
	}
	if b.Content != "" {
		block["content"] = b.Content
	}
	if b.IsError {
		block["is_error"] = true
	}
	return block
}

// marshalMessagesWithCacheCount is the shared implementation.
// cacheCount controls how many of the trailing user messages get cache_control.
func marshalMessagesWithCacheCount(messages []ChatMessage, cacheCount int) []any {
	// Find the indices of the last N user messages for cache marking.
	userIndices := make([]int, 0, cacheCount)
	for i := len(messages) - 1; i >= 0 && len(userIndices) < cacheCount; i-- {
		if messages[i].Role == "user" {
			userIndices = append(userIndices, i)
		}
	}
	cacheSet := make(map[int]bool, len(userIndices))
	for _, idx := range userIndices {
		cacheSet[idx] = true
	}

	result := make([]any, len(messages))
	for i, m := range messages {
		shouldCache := cacheSet[i]

		if len(m.ContentBlocks) > 0 {
			// Multi-block message (tool results, tool use, thinking blocks).
			// If caching, add cache_control to the last non-thinking block —
			// Anthropic rejects cache_control on thinking blocks, but a message
			// that ends with a thinking block (e.g. [text, thinking]) should
			// still cache its prior content.
			lastNonThinkingIdx := -1
			for j := len(m.ContentBlocks) - 1; j >= 0; j-- {
				if m.ContentBlocks[j].Type != "thinking" {
					lastNonThinkingIdx = j
					break
				}
			}
			blocks := make([]map[string]any, len(m.ContentBlocks))
			for j, b := range m.ContentBlocks {
				block := contentBlockToMap(b)
				if shouldCache && j == lastNonThinkingIdx {
					block["cache_control"] = map[string]string{"type": "ephemeral"}
				}
				blocks[j] = block
			}
			result[i] = map[string]any{
				"role":    m.Role,
				"content": blocks,
			}
		} else {
			// Simple text message.
			if shouldCache {
				result[i] = map[string]any{
					"role": m.Role,
					"content": []map[string]any{
						{
							"type": "text",
							"text": m.Content,
							"cache_control": map[string]string{
								"type": "ephemeral",
							},
						},
					},
				}
			} else {
				result[i] = map[string]any{
					"role":    m.Role,
					"content": m.Content,
				}
			}
		}
	}
	return result
}

// buildRequestBody assembles the anthropicRequest for the given ChatRequest.
// Shared between StreamChat (stream=true) and EstimateCacheablePrefix
// (stream=false, payload is marshalled but not sent) so the two stay in sync.
func (a *Anthropic) buildRequestBody(in ChatRequest, model string, interleavedThinking bool, reasoningCfg ReasoningConfig, stream bool) anthropicRequest {
	maxTokens := in.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 16384
	}
	body := anthropicRequest{
		Model:     model,
		MaxTokens: maxTokens,
		System:    a.buildSystemFromRequest(in),
		Messages:  a.marshalMessages(in.Messages),
		Stream:    stream,
	}
	if len(in.Tools) > 0 {
		body.Tools = a.buildToolsWithCacheControl(in.Tools)
	}
	// F3: attach thinking_config whenever the interleavedThinking gate is open.
	// The gate already requires BudgetTokens > 0, so the pair (header + config)
	// is always sent together.
	if interleavedThinking {
		body.ThinkingConfig = &anthropicThinkingConfig{
			Type:         "enabled",
			BudgetTokens: reasoningCfg.BudgetTokens,
		}
	}
	return body
}

// EstimateCacheablePrefix implements provider.Cacheable. It builds the same
// anthropicRequest payload StreamChat would send, then returns the byte offset
// of the last cache_control marker divided by 4 (token approximation). Returns
// 0 when no cache hints are configured or no marker would be emitted.
//
// Stays in lock-step with the rate-budget pre-flight in StreamChat (see
// computeCacheablePrefixBytes call site): both consume the same payload bytes
// and the same heuristic, just expressed in different units. Future readers
// changing one should change the other together.
func (a *Anthropic) EstimateCacheablePrefix(ctx context.Context, in ChatRequest) int {
	if len(a.cacheHints) == 0 {
		return 0
	}
	model := in.Model
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	reasoningCfg := ReasoningConfigFromContext(ctx)
	interleavedThinking := shouldEnableInterleavedThinking(reasoningCfg, model)
	body := a.buildRequestBody(in, model, interleavedThinking, reasoningCfg, true)
	payload, err := json.Marshal(body)
	if err != nil {
		return 0
	}
	return computeCacheablePrefixBytes(payload, a.cacheHints) / 4
}

// StreamChat implements Provider.StreamChat using Anthropic's streaming SSE API.
func (a *Anthropic) StreamChat(ctx context.Context, in ChatRequest) (<-chan StreamEvent, error) {
	ctx, span := otel.StartSpan(ctx, "nanite.provider.anthropic.stream")
	span.SetAttributes(
		attribute.String("nanite.provider", "anthropic"),
		attribute.String("nanite.model", in.Model),
		attribute.Int("nanite.messages.count", len(in.Messages)),
		attribute.Int("nanite.tools.count", len(in.Tools)),
		attribute.Int("nanite.slots.count", len(in.SlotBlocks)),
	)

	if a.apiKey == "" {
		span.SetStatus(codes.Error, "ANTHROPIC_API_KEY not set")
		span.End()
		return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	model := in.Model
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	// F3 (CW-20260420-0023): read reasoning config from context and decide
	// whether to enable interleaved thinking. shouldEnableInterleavedThinking
	// gates header + thinking_config as a pair (see helper docs).
	reasoningCfg := ReasoningConfigFromContext(ctx)
	interleavedThinking := shouldEnableInterleavedThinking(reasoningCfg, model)

	body := a.buildRequestBody(in, model, interleavedThinking, reasoningCfg, true)

	// anthropicRequest is fully built; remainder of function constructs the
	// HTTP call and spawns the SSE reader.
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Check if the circuit breaker is open before attempting.
	if a.CircuitBreaker != nil && a.CircuitBreaker.IsOpen() {
		span.SetStatus(codes.Error, "circuit breaker open")
		span.End()
		return nil, fmt.Errorf("circuit breaker open: provider rate limited after multiple retries")
	}

	requestStart := time.Now()

	// Rate-limit pacing: estimate request tokens and wait if necessary.
	if a.RateTracker != nil {
		estimatedTokens := len(payload) / 4
		cacheableBytes := 0
		markerOffset := -1
		// Cache-aware: subtract bytes covered by the cacheable prefix so a
		// pre-cache-hit second turn doesn't trigger ErrRequestExceedsRateBudget
		// from local pessimism. RateTracker.Record from response usage is
		// authoritative; this only affects the pre-flight estimate.
		if len(a.cacheHints) > 0 {
			cacheableBytes = computeCacheablePrefixBytes(payload, a.cacheHints)
			markerOffset = bytes.LastIndex(payload, []byte(`"cache_control"`))
			estimatedTokens -= cacheableBytes / 4
			if estimatedTokens < 0 {
				estimatedTokens = 0
			}
		}
		avail, limit := a.RateTracker.Remaining()
		slog.Debug("provider: rate budget preflight",
			"provider", "anthropic",
			"estimated_tokens", estimatedTokens,
			"cacheable_bytes", cacheableBytes,
			"marker_offset", markerOffset,
			"payload_bytes", len(payload),
			"available_tpm", avail,
		)
		// If the request alone is larger than the per-minute window, no
		// amount of waiting can fit it — signal the caller to compact
		// history instead of repeating 58s waits until the outer context
		// deadline fires.
		if limit > 0 && estimatedTokens > limit {
			log.Printf("provider: request ~%d tokens exceeds per-minute rate budget %d — signalling caller to compact",
				estimatedTokens, limit)
			span.SetStatus(codes.Error, "request exceeds rate budget")
			span.End()
			return nil, fmt.Errorf("%w: estimated %d tokens vs %d limit",
				ErrRequestExceedsRateBudget, estimatedTokens, limit)
		}
		if wait := a.RateTracker.WaitTime(estimatedTokens); wait > 0 {
			log.Printf("provider: pacing — waiting %s for rate limit budget (est. %d tokens, available %d/%d)",
				wait.Round(time.Millisecond), estimatedTokens, avail, limit)
			if err := PacingWait(ctx, wait, a.OnStatus); err != nil {
				return nil, fmt.Errorf("context cancelled during rate limit wait: %w", err)
			}
		}
	}

	// Retry loop with exponential backoff for rate limits and server errors.
	var resp *http.Response
	var lastErr error
	for attempt := 0; attempt <= a.Retry.MaxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", anthropicAPI, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", a.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		// F3 (CW-20260420-0023): append the interleaved-thinking beta header when
		// the model and effort level support it; baseline prompt-caching is always set.
		betaHeader := anthropicBetaBase
		if interleavedThinking {
			betaHeader += "," + InterleavedThinkingBetaHeader
		}
		req.Header.Set("anthropic-beta", betaHeader)

		resp, err = a.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("send request: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			if a.CircuitBreaker != nil {
				a.CircuitBreaker.RecordSuccess()
			}
			// Calibrate rate tracker from response headers.
			a.calibrateRateTracker(resp)
			break // success
		}

		// Read the error body.
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		apiErr := &APIError{
			StatusCode: resp.StatusCode,
			Message:    string(errBody),
			RetryAfter: ParseRetryAfter(resp.Header.Get("Retry-After")),
		}

		if !RetryableStatusCode(resp.StatusCode) || attempt == a.Retry.MaxRetries {
			// Record failure on circuit breaker when retries are exhausted.
			if a.CircuitBreaker != nil && attempt == a.Retry.MaxRetries {
				if tripped := a.CircuitBreaker.RecordFailure(); tripped {
					log.Printf("provider: circuit breaker tripped after consecutive failures")
					if a.OnCircuitOpen != nil {
						a.OnCircuitOpen()
					}
				}
			}
			span.RecordError(apiErr)
			span.SetStatus(codes.Error, apiErr.Error())
			span.SetAttributes(attribute.Int("nanite.http.status", resp.StatusCode))
			span.End()
			return nil, apiErr
		}

		// Calculate delay.
		delay := a.Retry.BackoffDelay(attempt, apiErr.RetryAfter)
		log.Printf("provider: retryable error %d (attempt %d/%d), retrying in %s",
			resp.StatusCode, attempt+1, a.Retry.MaxRetries, delay)

		if a.OnStatus != nil {
			a.OnStatus(fmt.Sprintf("Rate limited, retrying in %s... (attempt %d/%d)",
				delay.Round(time.Millisecond), attempt+1, a.Retry.MaxRetries))
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled during retry: %w", ctx.Err())
		case <-time.After(delay):
		}

		lastErr = apiErr
	}
	_ = lastErr

	span.SetAttributes(
		attribute.Int64("nanite.provider.latency_ms", time.Since(requestStart).Milliseconds()),
		attribute.Int("nanite.http.status", resp.StatusCode),
	)

	ch := make(chan StreamEvent, 64)
	go a.readSSEWithTracking(ctx, resp.Body, ch, span, interleavedThinking)
	return ch, nil
}

// computeCacheablePrefixBytes approximates the byte size of the cached
// prefix in payload as the offset of the last "cache_control" marker.
// The hints argument gates the computation: with no hints, the marshalers
// emit no markers and the heuristic returns 0.
//
// Heuristic: anthropicRequest serializes "tools" after "messages", so the
// last marker often sits inside tools and over-counts the cached prefix
// by the trailing user message bytes. Acceptable here because the result
// only feeds the pre-flight rate-budget estimate; runtime usage is
// recorded from the API response by readSSEWithTracking → Record.
func computeCacheablePrefixBytes(payload []byte, hints []CacheHint) int {
	if len(hints) == 0 {
		return 0
	}
	idx := bytes.LastIndex(payload, []byte(`"cache_control"`))
	if idx < 0 {
		return 0
	}
	return idx
}

// calibrateRateTracker reads Anthropic rate-limit headers and updates the tracker.
// Logs only when the calibrated limit actually changes (first calibration or a
// real tier transition); same-value re-calibrations are silent so the log
// signal stays meaningful instead of firing on every response.
func (a *Anthropic) calibrateRateTracker(resp *http.Response) {
	if a.RateTracker == nil {
		return
	}
	limitStr := resp.Header.Get("x-ratelimit-limit-input-tokens")
	if limitStr == "" {
		return
	}
	newLimit, err := strconv.Atoi(limitStr)
	if err != nil || newLimit <= 0 {
		return
	}
	_, oldLimit := a.RateTracker.Remaining()
	if oldLimit == newLimit {
		return
	}
	a.RateTracker.UpdateLimit(newLimit)

	remainingTPM := 0
	if v, err := strconv.Atoi(resp.Header.Get("x-ratelimit-remaining-input-tokens")); err == nil {
		remainingTPM = v
	}
	resetAt := resp.Header.Get("x-ratelimit-reset-input-tokens")

	slog.Info("provider: rate limit calibrated",
		"provider", "anthropic",
		"old_limit_tpm", oldLimit,
		"new_limit_tpm", newLimit,
		"remaining_tpm", remainingTPM,
		"reset_at", resetAt,
	)
}

// RateLimitTPM returns the live input-tokens-per-minute limit from the rate
// tracker, or 0 when no tracker is configured. Implements provider.RateLimited
// so callers can read the calibrated limit for telemetry without depending on
// the concrete *Anthropic type.
func (a *Anthropic) RateLimitTPM() int {
	if a.RateTracker == nil {
		return 0
	}
	_, limit := a.RateTracker.Remaining()
	return limit
}

// readSSEWithTracking wraps readSSE to record input tokens via the rate tracker
// and finalize the provider span with token counts.
// interleavedThinking signals whether thinking_delta events should be parsed.
func (a *Anthropic) readSSEWithTracking(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent, span trace.Span, interleavedThinking bool) {
	// Create an intermediary channel to intercept usage events.
	inner := make(chan StreamEvent, 64)
	go a.readSSE(ctx, body, inner, interleavedThinking)

	var totalInput, totalOutput int
	defer func() {
		close(ch)
		if span != nil {
			span.SetAttributes(
				attribute.Int("nanite.provider.input_tokens", totalInput),
				attribute.Int("nanite.provider.output_tokens", totalOutput),
			)
			span.End()
		}
	}()
	for ev := range inner {
		// Record input tokens in the rate tracker when we see usage from message_start.
		if ev.Type == EventUsage && ev.Usage != nil {
			if ev.Usage.InputTokens > 0 {
				totalInput += ev.Usage.InputTokens
				if a.RateTracker != nil {
					a.RateTracker.Record(ev.Usage.InputTokens)
					avail, limit := a.RateTracker.Remaining()
					log.Printf("provider: recorded %d input tokens (rate budget: %d/%d)", ev.Usage.InputTokens, avail, limit)
				}
			}
			if ev.Usage.OutputTokens > 0 {
				totalOutput += ev.Usage.OutputTokens
			}
		}
		ch <- ev
	}
}

// toolUseAccumulator tracks state for an in-progress tool_use content block.
type toolUseAccumulator struct {
	id        string
	name      string
	inputJSON strings.Builder
}

// readSSE parses the SSE stream from Anthropic and emits StreamEvents.
func (a *Anthropic) readSSE(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent, interleavedThinking bool) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	// Increase buffer size for large tool input JSON.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var eventType string
	var currentToolUse *toolUseAccumulator
	// F3 (CW-20260420-0023): thinking block accumulator. One per content block.
	var currentThinking *thinkingAccumulator
	var currentBlockIdx int
	_ = currentBlockIdx // tracked for correlation

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			ch <- StreamEvent{Type: EventError, Error: "context cancelled"}
			return
		default:
		}

		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			a.handleSSEData(eventType, data, ch, &currentToolUse, &currentThinking, &currentBlockIdx, interleavedThinking)
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Type: EventError, Error: fmt.Sprintf("read stream: %v", err)}
	}
}

// thinkingAccumulator accumulates the thinking text and signature for one
// interleaved thinking content block. Signature arrives in content_block_start
// for redacted thinking; for standard thinking blocks it comes in the final
// content_block_stop payload. In practice, thinking deltas arrive via
// thinking_delta events and the signature via signature_delta events.
// F3 / CW-20260420-0023.
type thinkingAccumulator struct {
	thinking  strings.Builder
	signature string
}

// handleSSEData processes a single SSE data payload based on event type.
// interleavedThinking gates parsing of thinking_delta / signature_delta events.
func (a *Anthropic) handleSSEData(eventType, data string, ch chan<- StreamEvent, currentToolUse **toolUseAccumulator, currentThinking **thinkingAccumulator, currentBlockIdx *int, interleavedThinking bool) {
	switch eventType {
	case "content_block_start":
		var payload struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type      string `json:"type"`
				ID        string `json:"id,omitempty"`
				Name      string `json:"name,omitempty"`
				Text      string `json:"text,omitempty"`
				Signature string `json:"signature,omitempty"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return
		}
		*currentBlockIdx = payload.Index
		switch payload.ContentBlock.Type {
		case "tool_use":
			*currentToolUse = &toolUseAccumulator{
				id:   payload.ContentBlock.ID,
				name: payload.ContentBlock.Name,
			}
			*currentThinking = nil
		case "thinking":
			if interleavedThinking {
				acc := &thinkingAccumulator{}
				// Signature may arrive here (unlikely for standard thinking) or
				// via signature_delta events later.
				acc.signature = payload.ContentBlock.Signature
				*currentThinking = acc
			}
			*currentToolUse = nil
		default:
			*currentToolUse = nil
			*currentThinking = nil
		}

	case "content_block_delta":
		var payload struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json,omitempty"`
				Thinking    string `json:"thinking,omitempty"`    // thinking_delta
				Signature   string `json:"signature,omitempty"`   // signature_delta
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return
		}

		switch payload.Delta.Type {
		case "text_delta":
			ch <- StreamEvent{Type: EventDelta, Content: payload.Delta.Text}
		case "input_json_delta":
			if *currentToolUse != nil {
				(*currentToolUse).inputJSON.WriteString(payload.Delta.PartialJSON)
			}
		case "thinking_delta":
			// F3 (CW-20260420-0023): accumulate thinking content.
			if interleavedThinking && *currentThinking != nil {
				(*currentThinking).thinking.WriteString(payload.Delta.Thinking)
			}
		case "signature_delta":
			// F3: signature arrives as a separate delta event; append to the accumulator.
			if interleavedThinking && *currentThinking != nil {
				(*currentThinking).signature += payload.Delta.Signature
			}
		}

	case "content_block_stop":
		if *currentToolUse != nil {
			tu := *currentToolUse
			var input map[string]any
			raw := tu.inputJSON.String()
			if raw != "" {
				if err := json.Unmarshal([]byte(raw), &input); err != nil {
					// If JSON parsing fails, send the raw string as a single "input" key.
					input = map[string]any{"_raw": raw}
				}
			} else {
				input = map[string]any{}
			}
			ch <- StreamEvent{
				Type: EventToolUse,
				ToolUse: &ToolUseBlock{
					ID:    tu.id,
					Name:  tu.name,
					Input: input,
				},
			}
			*currentToolUse = nil
		}
		// F3 (CW-20260420-0023): emit completed thinking block with signature.
		if interleavedThinking && *currentThinking != nil {
			acc := *currentThinking
			ch <- StreamEvent{
				Type: EventThinking,
				ThinkingBlock: &ThinkingBlock{
					Thinking:  acc.thinking.String(),
					Signature: acc.signature,
				},
			}
			*currentThinking = nil
		}

	case "message_delta":
		var payload struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err == nil {
			ch <- StreamEvent{
				Type: EventUsage,
				Usage: &Usage{
					OutputTokens: payload.Usage.OutputTokens,
					StopReason:   payload.Delta.StopReason,
				},
			}
		}

	case "message_start":
		var payload struct {
			Message struct {
				Usage struct {
					InputTokens         int `json:"input_tokens"`
					CacheCreationTokens int `json:"cache_creation_input_tokens"`
					CacheReadTokens     int `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err == nil {
			u := payload.Message.Usage
			if u.CacheCreationTokens > 0 || u.CacheReadTokens > 0 {
				log.Printf("provider: prompt cache — creation=%d read=%d input=%d",
					u.CacheCreationTokens, u.CacheReadTokens, u.InputTokens)
			}
			ch <- StreamEvent{
				Type: EventUsage,
				Usage: &Usage{
					InputTokens:         u.InputTokens,
					CacheCreationTokens: u.CacheCreationTokens,
					CacheReadTokens:     u.CacheReadTokens,
				},
			}
		}

	case "message_stop":
		ch <- StreamEvent{Type: EventDone}

	case "error":
		var payload struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err == nil {
			ch <- StreamEvent{Type: EventError, Error: payload.Error.Message}
		} else {
			ch <- StreamEvent{Type: EventError, Error: data}
		}
	}
}

// Complete makes a non-streaming completion call.
func (a *Anthropic) Complete(ctx context.Context, in ChatRequest) (string, error) {
	result, err := a.CompleteWithUsage(ctx, in)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// CompleteWithUsage makes a non-streaming completion call and preserves usage metadata.
func (a *Anthropic) CompleteWithUsage(ctx context.Context, in ChatRequest) (CompleteResult, error) {
	ctx, span := otel.StartSpan(ctx, "nanite.provider.anthropic.complete")
	defer span.End()
	span.SetAttributes(
		attribute.String("nanite.provider", "anthropic"),
		attribute.String("nanite.model", in.Model),
		attribute.Int("nanite.messages.count", len(in.Messages)),
		attribute.Int("nanite.slots.count", len(in.SlotBlocks)),
	)

	if a.apiKey == "" {
		span.SetStatus(codes.Error, "ANTHROPIC_API_KEY not set")
		return CompleteResult{}, fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	model := in.Model
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	// Historically MaxTokens was hardcoded to 128 here, which silently truncated
	// non-streaming completions for any caller that emitted more than ~512 bytes
	// of output. Caller-supplied MaxTokens now wins; default mirrors the
	// streaming path (16384) so the two code paths agree on what "no cap given"
	// means.
	maxTokens := in.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 16384
	}
	body := anthropicRequest{
		Model:     model,
		MaxTokens: maxTokens,
		System:    a.buildSystemFromRequest(in),
		Messages:  a.marshalMessages(in.Messages),
		Stream:    false,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return CompleteResult{}, fmt.Errorf("marshal request: %w", err)
	}

	// Retry loop with exponential backoff for rate limits and server errors.
	var resp *http.Response
	for attempt := 0; attempt <= a.Retry.MaxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", anthropicAPI, bytes.NewReader(payload))
		if err != nil {
			return CompleteResult{}, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", a.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")

		resp, err = a.client.Do(req)
		if err != nil {
			return CompleteResult{}, fmt.Errorf("send request: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			break // success — fall through to decode
		}

		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		apiErr := &APIError{
			StatusCode: resp.StatusCode,
			Message:    string(errBody),
			RetryAfter: ParseRetryAfter(resp.Header.Get("Retry-After")),
		}

		if !RetryableStatusCode(resp.StatusCode) || attempt == a.Retry.MaxRetries {
			return CompleteResult{}, apiErr
		}

		delay := a.Retry.BackoffDelay(attempt, apiErr.RetryAfter)
		log.Printf("provider: retryable error %d (attempt %d/%d), retrying in %s",
			resp.StatusCode, attempt+1, a.Retry.MaxRetries, delay)

		select {
		case <-ctx.Done():
			return CompleteResult{}, fmt.Errorf("context cancelled during retry: %w", ctx.Err())
		case <-time.After(delay):
		}
	}
	defer resp.Body.Close()

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens         int `json:"input_tokens"`
			OutputTokens        int `json:"output_tokens"`
			CacheCreationTokens int `json:"cache_creation_input_tokens"`
			CacheReadTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return CompleteResult{}, fmt.Errorf("decode response: %w", err)
	}

	out := CompleteResult{
		Usage: &Usage{
			InputTokens:         result.Usage.InputTokens,
			OutputTokens:        result.Usage.OutputTokens,
			CacheCreationTokens: result.Usage.CacheCreationTokens,
			CacheReadTokens:     result.Usage.CacheReadTokens,
			StopReason:          result.StopReason,
		},
	}
	for _, block := range result.Content {
		if block.Type == "text" {
			out.Text = strings.TrimSpace(block.Text)
			return out, nil
		}
	}
	return out, nil
}

// Capabilities returns the capabilities supported by the Anthropic provider.
func (a *Anthropic) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		SupportsStreamJSON:          true,   // Anthropic supports streaming with tool use
		SupportsPreToolHooks:        false,  // No direct pre-tool hook support
		SupportsPostToolHooks:       false,  // No direct post-tool hook support
		SupportsSystemPromptCaching: true,   // Anthropic supports prompt caching
		SupportsToolCalling:         true,   // Anthropic supports function calling
		SupportsBatch:               false,  // No batch API support in current implementation
		SupportsImageInput:          true,   // Anthropic supports image inputs
		MaxTokens:                   16384,  // Claude max output tokens (default)
		ContextWindowSize:           200000, // Claude models support 200k context window
	}
}
