package ai

import "testing"

func TestModelRegistry(t *testing.T) {
	// Clean up for test isolation
	modelRegistryMu.Lock()
	saved := modelRegistry
	modelRegistry = make(map[Provider]map[string]*Model)
	modelRegistryMu.Unlock()
	defer func() {
		modelRegistryMu.Lock()
		modelRegistry = saved
		modelRegistryMu.Unlock()
	}()

	model := &Model{
		ID:       "test-model",
		Name:     "Test Model",
		ApiType:  "test-api",
		Provider: "test-provider",
		BaseURL:  "https://test.api.com",
	}

	RegisterModel("test-provider", model)

	got := GetModel("test-provider", "test-model")
	if got == nil {
		t.Fatal("expected model to be registered")
	}
	if got.ID != "test-model" {
		t.Errorf("expected 'test-model', got %q", got.ID)
	}

	// Non-existent
	if GetModel("test-provider", "non-existent") != nil {
		t.Error("expected nil for non-existent model")
	}
	if GetModel("non-existent", "test-model") != nil {
		t.Error("expected nil for non-existent provider")
	}

	// GetModels
	models := GetModels("test-provider")
	if len(models) != 1 {
		t.Errorf("expected 1 model, got %d", len(models))
	}

	// GetProviders
	providers := GetProviders()
	if len(providers) != 1 {
		t.Errorf("expected 1 provider, got %d", len(providers))
	}
}

func TestCalculateCost(t *testing.T) {
	model := &Model{
		Cost: ModelCost{
			Input:      3.0,  // $3/million
			Output:     15.0, // $15/million
			CacheRead:  0.3,
			CacheWrite: 3.75,
		},
	}

	usage := &Usage{
		Input:      1000,
		Output:     500,
		CacheRead:  200,
		CacheWrite: 100,
	}

	CalculateCost(model, usage)

	expected := 3.0/1000000*1000 + 15.0/1000000*500 + 0.3/1000000*200 + 3.75/1000000*100
	if abs(usage.Cost.Total-expected) > 0.0001 {
		t.Errorf("expected total cost %f, got %f", expected, usage.Cost.Total)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func TestSupportsXHigh(t *testing.T) {
	tests := []struct {
		model    *Model
		expected bool
	}{
		{&Model{ID: "gpt-5.2-turbo", ApiType: "openai"}, true},
		{&Model{ID: "gpt-5.3", ApiType: "openai"}, true},
		{&Model{ID: "claude-opus-4-6", ApiType: "anthropic-messages"}, true},
		{&Model{ID: "claude-opus-4.6", ApiType: "anthropic-messages"}, true},
		{&Model{ID: "claude-sonnet-4", ApiType: "anthropic-messages"}, false},
		{&Model{ID: "gpt-4o", ApiType: "openai"}, false},
	}

	for _, tt := range tests {
		got := SupportsXHigh(tt.model)
		if got != tt.expected {
			t.Errorf("SupportsXHigh(%q): expected %v, got %v", tt.model.ID, tt.expected, got)
		}
	}
}

func TestModelsAreEqual(t *testing.T) {
	a := &Model{ID: "model-1", Provider: "provider-1"}
	b := &Model{ID: "model-1", Provider: "provider-1"}
	c := &Model{ID: "model-2", Provider: "provider-1"}

	if !ModelsAreEqual(a, b) {
		t.Error("expected models to be equal")
	}
	if ModelsAreEqual(a, c) {
		t.Error("expected models to not be equal")
	}
	if ModelsAreEqual(nil, b) {
		t.Error("expected nil != model")
	}
	if ModelsAreEqual(a, nil) {
		t.Error("expected model != nil")
	}
}
