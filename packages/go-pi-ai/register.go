package ai

// RegisterBuiltInProviders registers all built-in API providers.
func RegisterBuiltInProviders() {
	RegisterApiProvider(&ApiProvider{
		ApiType: string(ApiAnthropicMessages),
		Stream: func(model *Model, ctx *Context, options *StreamOptions) *AssistantMessageEventStream {
			opts := &AnthropicOptions{}
			if options != nil {
				opts.StreamOptions = *options
			}
			return StreamAnthropic(model, ctx, opts)
		},
		StreamSimple: StreamSimpleAnthropic,
	})

	RegisterApiProvider(&ApiProvider{
		ApiType:      string(ApiOpenAICompletions),
		Stream:       StreamOpenAICompletions,
		StreamSimple: StreamSimpleOpenAICompletions,
	})
}

// ResetApiProviders clears all providers and re-registers the built-in ones.
func ResetApiProviders() {
	ClearApiProviders()
	RegisterBuiltInProviders()
}

func init() {
	RegisterBuiltInProviders()
}
