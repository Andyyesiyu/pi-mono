package ai

import "fmt"

// Stream creates a streaming LLM call using the provider registered for the model's API.
func Stream(model *Model, ctx *Context, options *StreamOptions) (*AssistantMessageEventStream, error) {
	provider := GetApiProvider(model.ApiType)
	if provider == nil {
		return nil, fmt.Errorf("no API provider registered for api: %s", model.ApiType)
	}
	return provider.Stream(model, ctx, options), nil
}

// Complete makes a streaming LLM call and waits for the full result.
func Complete(model *Model, ctx *Context, options *StreamOptions) (*AssistantMessage, error) {
	s, err := Stream(model, ctx, options)
	if err != nil {
		return nil, err
	}
	return s.Result(), nil
}

// StreamSimple creates a streaming LLM call with simplified options (reasoning support).
func StreamSimple(model *Model, ctx *Context, options *SimpleStreamOptions) (*AssistantMessageEventStream, error) {
	provider := GetApiProvider(model.ApiType)
	if provider == nil {
		return nil, fmt.Errorf("no API provider registered for api: %s", model.ApiType)
	}
	return provider.StreamSimple(model, ctx, options), nil
}

// CompleteSimple makes a streaming LLM call with simplified options and waits for the full result.
func CompleteSimple(model *Model, ctx *Context, options *SimpleStreamOptions) (*AssistantMessage, error) {
	s, err := StreamSimple(model, ctx, options)
	if err != nil {
		return nil, err
	}
	return s.Result(), nil
}
