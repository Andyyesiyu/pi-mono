package ai

import "os"

// envKeyMap maps provider names to their environment variable names.
var envKeyMap = map[Provider]string{
	string(ProviderAnthropic):        "ANTHROPIC_API_KEY",
	string(ProviderOpenAI):           "OPENAI_API_KEY",
	string(ProviderGoogle):           "GOOGLE_API_KEY",
	string(ProviderGoogleGeminiCli):  "GOOGLE_API_KEY",
	string(ProviderGoogleVertex):     "GOOGLE_API_KEY",
	string(ProviderXAI):              "XAI_API_KEY",
	string(ProviderGroq):             "GROQ_API_KEY",
	string(ProviderCerebras):         "CEREBRAS_API_KEY",
	string(ProviderOpenRouter):       "OPENROUTER_API_KEY",
	string(ProviderZAI):              "ZAI_API_KEY",
	string(ProviderMistral):          "MISTRAL_API_KEY",
	string(ProviderMiniMax):          "MINIMAX_API_KEY",
	string(ProviderMiniMaxCN):        "MINIMAX_API_KEY",
	string(ProviderHuggingFace):      "HUGGINGFACE_API_KEY",
	string(ProviderGithubCopilot):    "GITHUB_COPILOT_TOKEN",
}

// GetEnvApiKey returns the API key from environment variables for the given provider.
func GetEnvApiKey(provider Provider) string {
	envVar, ok := envKeyMap[provider]
	if !ok {
		return ""
	}
	return os.Getenv(envVar)
}
