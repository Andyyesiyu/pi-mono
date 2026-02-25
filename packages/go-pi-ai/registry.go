package ai

import "sync"

// ApiProvider defines the interface for an LLM API provider.
type ApiProvider struct {
	ApiType      Api
	Stream       StreamFunction
	StreamSimple SimpleStreamFunction
}

var (
	registryMu sync.RWMutex
	registry   = make(map[Api]*ApiProvider)
)

// RegisterApiProvider registers an API provider for a given API type.
func RegisterApiProvider(provider *ApiProvider) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[provider.ApiType] = provider
}

// GetApiProvider returns the API provider for the given API type.
func GetApiProvider(api Api) *ApiProvider {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registry[api]
}

// GetApiProviders returns all registered API providers.
func GetApiProviders() []*ApiProvider {
	registryMu.RLock()
	defer registryMu.RUnlock()
	providers := make([]*ApiProvider, 0, len(registry))
	for _, p := range registry {
		providers = append(providers, p)
	}
	return providers
}

// ClearApiProviders removes all registered API providers.
func ClearApiProviders() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = make(map[Api]*ApiProvider)
}
