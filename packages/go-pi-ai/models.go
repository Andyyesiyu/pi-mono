package ai

import (
	"strings"
	"sync"
)

var (
	modelRegistryMu sync.RWMutex
	modelRegistry   = make(map[Provider]map[string]*Model)
)

// RegisterModel registers a model for a provider.
func RegisterModel(provider Provider, model *Model) {
	modelRegistryMu.Lock()
	defer modelRegistryMu.Unlock()
	if _, ok := modelRegistry[provider]; !ok {
		modelRegistry[provider] = make(map[string]*Model)
	}
	modelRegistry[provider][model.ID] = model
}

// GetModel returns a registered model by provider and model ID.
func GetModel(provider Provider, modelID string) *Model {
	modelRegistryMu.RLock()
	defer modelRegistryMu.RUnlock()
	providerModels, ok := modelRegistry[provider]
	if !ok {
		return nil
	}
	return providerModels[modelID]
}

// GetProviders returns all registered providers.
func GetProviders() []Provider {
	modelRegistryMu.RLock()
	defer modelRegistryMu.RUnlock()
	providers := make([]Provider, 0, len(modelRegistry))
	for p := range modelRegistry {
		providers = append(providers, p)
	}
	return providers
}

// GetModels returns all models for a given provider.
func GetModels(provider Provider) []*Model {
	modelRegistryMu.RLock()
	defer modelRegistryMu.RUnlock()
	providerModels, ok := modelRegistry[provider]
	if !ok {
		return nil
	}
	models := make([]*Model, 0, len(providerModels))
	for _, m := range providerModels {
		models = append(models, m)
	}
	return models
}

// CalculateCost computes the monetary cost for the given usage based on model pricing.
func CalculateCost(model *Model, usage *Usage) {
	usage.Cost.Input = (model.Cost.Input / 1000000) * float64(usage.Input)
	usage.Cost.Output = (model.Cost.Output / 1000000) * float64(usage.Output)
	usage.Cost.CacheRead = (model.Cost.CacheRead / 1000000) * float64(usage.CacheRead)
	usage.Cost.CacheWrite = (model.Cost.CacheWrite / 1000000) * float64(usage.CacheWrite)
	usage.Cost.Total = usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
}

// SupportsXHigh checks if a model supports xhigh thinking level.
func SupportsXHigh(model *Model) bool {
	if strings.Contains(model.ID, "gpt-5.2") || strings.Contains(model.ID, "gpt-5.3") {
		return true
	}
	if model.ApiType == string(ApiAnthropicMessages) {
		return strings.Contains(model.ID, "opus-4-6") || strings.Contains(model.ID, "opus-4.6")
	}
	return false
}

// ModelsAreEqual checks if two models are equal by comparing id and provider.
func ModelsAreEqual(a, b *Model) bool {
	if a == nil || b == nil {
		return false
	}
	return a.ID == b.ID && a.Provider == b.Provider
}
