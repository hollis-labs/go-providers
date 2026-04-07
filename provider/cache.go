package provider

// CacheHint tells a provider what content to cache.
// Position identifies the type of content ("system", "tools", "recent_message").
// Index provides ordering context — for "recent_message", 0 means the most recent
// user message, 1 means the second most recent, etc.
type CacheHint struct {
	Position string // "system", "tools", "recent_message"
	Index    int    // ordering context (e.g. 0 = most recent, 1 = second most recent)
}

// CacheableProvider is implemented by providers that support prompt caching.
// The chat engine calls SetCacheHints before each provider request to tell the
// provider WHAT to cache. Each provider decides HOW to implement caching based
// on its own API (e.g., Anthropic uses cache_control: {type: "ephemeral"}).
type CacheableProvider interface {
	SetCacheHints(hints []CacheHint)
}

// DefaultCacheStrategy returns the standard set of cache hints used before
// each provider call. It caches:
//   - The system prompt
//   - The last tool definition (marking the end of the tools block)
//   - The last 2 user messages (most recent conversation context)
func DefaultCacheStrategy() []CacheHint {
	return []CacheHint{
		{Position: "system", Index: 0},
		{Position: "tools", Index: 0},
		{Position: "recent_message", Index: 0},
		{Position: "recent_message", Index: 1},
	}
}
