package provider

import (
	"fmt"
	"sync"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// BudgetViolation represents a detected budget violation.
type BudgetViolation struct {
	Type        string // "token_budget", "cost_budget"
	Description string
	Current     float64 // Current usage
	Limit       float64 // Budget limit
	Event       llmtypes.StreamEvent
}

func (bv *BudgetViolation) Error() string {
	return fmt.Sprintf("Budget violation (%s): %s (current: %.2f, limit: %.2f)",
		bv.Type, bv.Description, bv.Current, bv.Limit)
}

// CostMonitor tracks token usage and cost to ensure operations stay within budget.
type CostMonitor struct {
	tokenBudget        int     // Maximum tokens allowed
	costBudgetUSD      float64 // Maximum cost in USD
	budgetExceededMode string  // "log" or "kill"

	// Tracking state
	mu                sync.RWMutex
	totalInputTokens  int
	totalOutputTokens int
	totalCostUSD      float64

	// Provider-specific cost rates (tokens per dollar)
	costRates map[string]CostRate
}

// CostRate defines the cost structure for a provider.
type CostRate struct {
	InputTokensPerDollar  float64 // How many input tokens per USD
	OutputTokensPerDollar float64 // How many output tokens per USD
	Name                  string  // Provider name for logging
}

// NewCostMonitor creates a new cost monitor with the given budget limits.
func NewCostMonitor(tokenBudget int, costBudgetUSD float64, budgetExceededMode string) *CostMonitor {
	cm := &CostMonitor{
		tokenBudget:        tokenBudget,
		costBudgetUSD:      costBudgetUSD,
		budgetExceededMode: budgetExceededMode,
		costRates:          getDefaultCostRates(),
	}
	return cm
}

// getDefaultCostRates returns default cost rates keyed by underlying-model
// vendor. These are approximate rates as of 2026 and should be updated
// periodically. Consumers attach a key to the llmtypes.EventUsage stream from any
// adapter (HTTP-shape removed in v0.10.0; PTY/subprocess adapters that emit
// llmtypes.EventUsage still benefit when the wrapped CLI uses one of these models).
func getDefaultCostRates() map[string]CostRate {
	return map[string]CostRate{
		"anthropic": {
			InputTokensPerDollar:  333333, // ~$3.00 per 1M input tokens
			OutputTokensPerDollar: 66667,  // ~$15.00 per 1M output tokens
			Name:                  "Anthropic Claude",
		},
		"openai": {
			InputTokensPerDollar:  200000, // ~$5.00 per 1M input tokens
			OutputTokensPerDollar: 66667,  // ~$15.00 per 1M output tokens
			Name:                  "OpenAI GPT-4",
		},
		"ollama": {
			InputTokensPerDollar:  1000000000, // Essentially free for local models
			OutputTokensPerDollar: 1000000000, // Essentially free for local models
			Name:                  "Ollama (Local)",
		},
	}
}

// CheckEvent examines a stream event for budget violations.
func (cm *CostMonitor) CheckEvent(event llmtypes.StreamEvent) *BudgetViolation {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Update usage counters based on event type
	switch event.Type {
	case "usage", "done":
		if event.Usage != nil {
			return cm.updateUsageAndCheck(event)
		}
	}

	return nil
}

// updateUsageAndCheck updates usage counters and checks for budget violations.
func (cm *CostMonitor) updateUsageAndCheck(event llmtypes.StreamEvent) *BudgetViolation {
	usage := event.Usage

	// Update token counters
	cm.totalInputTokens += usage.InputTokens
	cm.totalOutputTokens += usage.OutputTokens

	// Estimate cost (we'd need provider context for accurate rates)
	// For now, use a reasonable default rate
	estimatedCost := cm.estimateCost(usage.InputTokens, usage.OutputTokens, "anthropic")
	cm.totalCostUSD += estimatedCost

	// Check token budget
	totalTokens := cm.totalInputTokens + cm.totalOutputTokens
	if cm.tokenBudget > 0 && totalTokens > cm.tokenBudget {
		return &BudgetViolation{
			Type:        "token_budget",
			Description: fmt.Sprintf("Token budget exceeded: %d/%d tokens", totalTokens, cm.tokenBudget),
			Current:     float64(totalTokens),
			Limit:       float64(cm.tokenBudget),
			Event:       event,
		}
	}

	// Check cost budget
	if cm.costBudgetUSD > 0 && cm.totalCostUSD > cm.costBudgetUSD {
		return &BudgetViolation{
			Type:        "cost_budget",
			Description: fmt.Sprintf("Cost budget exceeded: $%.4f/$%.2f", cm.totalCostUSD, cm.costBudgetUSD),
			Current:     cm.totalCostUSD,
			Limit:       cm.costBudgetUSD,
			Event:       event,
		}
	}

	return nil
}

// estimateCost calculates the estimated cost for the given token usage.
func (cm *CostMonitor) estimateCost(inputTokens, outputTokens int, provider string) float64 {
	rate, exists := cm.costRates[provider]
	if !exists {
		// Use anthropic as default
		rate = cm.costRates["anthropic"]
	}

	inputCost := float64(inputTokens) / rate.InputTokensPerDollar
	outputCost := float64(outputTokens) / rate.OutputTokensPerDollar

	return inputCost + outputCost
}

// GetUsageSummary returns a summary of current usage and costs.
func (cm *CostMonitor) GetUsageSummary() UsageSummary {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	return UsageSummary{
		TotalInputTokens:  cm.totalInputTokens,
		TotalOutputTokens: cm.totalOutputTokens,
		TotalTokens:       cm.totalInputTokens + cm.totalOutputTokens,
		TotalCostUSD:      cm.totalCostUSD,
		TokenBudget:       cm.tokenBudget,
		CostBudgetUSD:     cm.costBudgetUSD,
		TokenUtilization:  cm.calculateTokenUtilization(),
		CostUtilization:   cm.calculateCostUtilization(),
	}
}

// UsageSummary provides a summary of current usage and budget status.
type UsageSummary struct {
	TotalInputTokens  int
	TotalOutputTokens int
	TotalTokens       int
	TotalCostUSD      float64
	TokenBudget       int
	CostBudgetUSD     float64
	TokenUtilization  float64 // Percentage of token budget used
	CostUtilization   float64 // Percentage of cost budget used
}

// calculateTokenUtilization returns the percentage of token budget used.
func (cm *CostMonitor) calculateTokenUtilization() float64 {
	if cm.tokenBudget <= 0 {
		return 0.0
	}
	totalTokens := cm.totalInputTokens + cm.totalOutputTokens
	return (float64(totalTokens) / float64(cm.tokenBudget)) * 100.0
}

// calculateCostUtilization returns the percentage of cost budget used.
func (cm *CostMonitor) calculateCostUtilization() float64 {
	if cm.costBudgetUSD <= 0 {
		return 0.0
	}
	return (cm.totalCostUSD / cm.costBudgetUSD) * 100.0
}

// Reset resets all usage counters to zero.
func (cm *CostMonitor) Reset() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.totalInputTokens = 0
	cm.totalOutputTokens = 0
	cm.totalCostUSD = 0.0
}

// SetCostRate allows updating cost rates for specific providers.
func (cm *CostMonitor) SetCostRate(provider string, rate CostRate) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.costRates[provider] = rate
}
