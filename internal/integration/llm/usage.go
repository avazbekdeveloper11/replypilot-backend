// Package llm holds types shared across LLM provider adapters
// (internal/integration/geminiapi, internal/integration/ollamaapi) that
// internal/usecase/ai.UseCase needs without depending on any one
// provider's package. Before this existed, ai.Generator's signature
// referenced geminiapi.GenerateUsage directly, which meant "the AI reply
// usecase depends on Gemini specifically" even though its whole job is to
// be provider-agnostic (see ai.Generator's doc comment) — that only
// stayed invisible as long as Gemini was the only provider. Adding
// ollamaapi as a second one made the coupling a real problem: either
// ollamaapi imports geminiapi just to borrow a struct (wrong direction —
// providers shouldn't depend on each other), or this usecase package
// stays hardwired to Gemini. This package is the fix.
package llm

// Usage carries a provider's own reported token counts for one Generate
// call, plus which model actually produced the reply — real numbers from
// the provider's response where available (contrast
// internal/usecase/knowledgebase's chunkText, which estimates token count
// because that endpoint has no usage field).
type Usage struct {
	// Model is what actually generated this reply (e.g. "gemini-3.5-flash",
	// "qwen3:4b") — entity.AIResponse.ModelUsed is set from this, not from
	// a provider-package constant, specifically so switching providers
	// (internal/config's AI_PROVIDER) doesn't require touching
	// internal/usecase/ai at all.
	Model            string
	PromptTokens     int
	CompletionTokens int
}
