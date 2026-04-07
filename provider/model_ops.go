package provider

// Operation constants for model selection.
const (
	OpChat          = "chat"
	OpSummarization = "summarization"
)

// OperationModelConfig holds the provider+model pair for a specific operation.
// Configured externally (e.g., from user settings or defaults).
type OperationModelConfig struct {
	ProviderName string
	ModelID      string
}

// ModelSelector resolves the best provider+model for a given operation.
// The ContextWindow's compaction pipeline calls this to get a cheap model
// for summarization without needing to know where the config comes from.
type ModelSelector interface {
	ModelForOperation(op string) (providerName string, modelID string, ok bool)
}

// StaticModelSelector is a simple map-backed selector. Populated at startup
// from user settings and provider defaults.
type StaticModelSelector struct {
	ops      map[string]OperationModelConfig
	fallback OperationModelConfig // used when no op-specific config exists
}

// NewStaticModelSelector creates a selector with the given fallback.
func NewStaticModelSelector(fallbackProvider, fallbackModel string) *StaticModelSelector {
	return &StaticModelSelector{
		ops: make(map[string]OperationModelConfig),
		fallback: OperationModelConfig{
			ProviderName: fallbackProvider,
			ModelID:      fallbackModel,
		},
	}
}

// SetOperation configures a provider+model for the given operation.
func (s *StaticModelSelector) SetOperation(op, providerName, modelID string) {
	s.ops[op] = OperationModelConfig{
		ProviderName: providerName,
		ModelID:      modelID,
	}
}

// ModelForOperation returns the provider+model for the given operation.
// Falls back to the default (utility) provider/model when no op-specific
// config exists.
func (s *StaticModelSelector) ModelForOperation(op string) (string, string, bool) {
	if cfg, ok := s.ops[op]; ok && cfg.ProviderName != "" {
		return cfg.ProviderName, cfg.ModelID, true
	}
	if s.fallback.ProviderName != "" {
		return s.fallback.ProviderName, s.fallback.ModelID, true
	}
	return "", "", false
}
