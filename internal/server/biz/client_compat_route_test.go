package biz

import (
	"net/http"
	"testing"

	"github.com/looplj/axonhub/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecideClientRoute_GrokXAIPassthrough(t *testing.T) {
	settings := DefaultClientCompatSettings()
	settings.Enabled = true
	p := settings.Profiles[ClientProfileGrokBuild]
	p.Enabled = true
	settings.Profiles[ClientProfileGrokBuild] = p

	detect := &ClientDetectResult{ProfileID: ClientProfileGrokBuild}
	d := DecideClientRoute(&settings, detect, "xai_responses", llm.APIFormatOpenAIResponse)
	assert.Equal(t, "passthrough_xai", d.Reason)
	assert.False(t, d.InjectPromptCacheKey)
	assert.False(t, d.ResponsePatchActive)
}

func TestDecideClientRoute_GrokCodexTranslate(t *testing.T) {
	settings := DefaultClientCompatSettings()
	settings.Enabled = true
	p := settings.Profiles[ClientProfileGrokBuild]
	p.Enabled = true
	settings.Profiles[ClientProfileGrokBuild] = p

	detect := &ClientDetectResult{ProfileID: ClientProfileGrokBuild}
	d := DecideClientRoute(&settings, detect, "codex", llm.APIFormatOpenAIResponse)
	assert.Equal(t, "translate_responses", d.Reason)
	assert.True(t, d.InjectPromptCacheKey)
	assert.True(t, d.ResponsePatchActive)
	assert.True(t, d.ResponsePatches.AnyActive())
}

func TestDecideClientRoute_GrokOpenaiResponsesTranslate(t *testing.T) {
	settings := DefaultClientCompatSettings()
	settings.Enabled = true
	p := settings.Profiles[ClientProfileGrokBuild]
	p.Enabled = true
	settings.Profiles[ClientProfileGrokBuild] = p

	detect := &ClientDetectResult{ProfileID: ClientProfileGrokBuild}
	d := DecideClientRoute(&settings, detect, "openai_responses", llm.APIFormatOpenAIResponse)
	assert.Equal(t, "translate_responses", d.Reason)
	assert.True(t, d.InjectPromptCacheKey)
	assert.True(t, d.ResponsePatchActive)
}

func TestDecideClientRoute_DisabledProfile(t *testing.T) {
	settings := DefaultClientCompatSettings()
	settings.Enabled = true
	detect := &ClientDetectResult{ProfileID: ClientProfileGrokBuild}
	d := DecideClientRoute(&settings, detect, "codex", llm.APIFormatOpenAIResponse)
	assert.Equal(t, "none", d.Reason)
}

func TestInjectPromptCacheKeyJSON(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-luna","stream":true}`)
	out, ok := InjectPromptCacheKeyJSON(body, "sess-123")
	require.True(t, ok)
	require.Contains(t, string(out), `"prompt_cache_key":"sess-123"`)

	// Client-provided non-auto key — leave alone
	out2, ok2 := InjectPromptCacheKeyJSON(out, "other")
	assert.False(t, ok2)
	assert.Equal(t, string(out), string(out2))

	// Empty key
	_, ok3 := InjectPromptCacheKeyJSON(body, "")
	assert.False(t, ok3)

	// Replace AxonHub auto key (at-…-anchor) with Grok session id
	auto := []byte(`{"model":"x","prompt_cache_key":"at-e999c3b3-997b-4060-aeb5-58cb37385a1f-5c38cb53722ffb11"}`)
	out4, ok4 := InjectPromptCacheKeyJSON(auto, "019fbce5-9e2d-7391-9507-64b1cb5f24db")
	require.True(t, ok4)
	require.Contains(t, string(out4), `"prompt_cache_key":"019fbce5-9e2d-7391-9507-64b1cb5f24db"`)
	require.NotContains(t, string(out4), `"at-`)
}

func TestIsAxonHubAutoPromptCacheKey(t *testing.T) {
	assert.True(t, IsAxonHubAutoPromptCacheKey("at-e999c3b3-997b-4060-aeb5-58cb37385a1f"))
	assert.True(t, IsAxonHubAutoPromptCacheKey("at-e999c3b3-997b-4060-aeb5-58cb37385a1f-5c38cb53722ffb11"))
	assert.False(t, IsAxonHubAutoPromptCacheKey("019fbce5-9e2d-7391-9507-64b1cb5f24db"))
	assert.False(t, IsAxonHubAutoPromptCacheKey(""))
}

func TestGrokSessionIDFromHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("X-Grok-Session-Id", "019fbd1e-e7c4-7e11-98b4-a391d59ffb84")
	h.Set("X-Grok-Conv-Id", "recap-abc")
	assert.Equal(t, "019fbd1e-e7c4-7e11-98b4-a391d59ffb84", GrokSessionIDFromHeaders(h))

	h2 := http.Header{}
	h2.Set("X-Grok-Conv-Id", "recap-only")
	assert.Equal(t, "", GrokSessionIDFromHeaders(h2), "recap conv id alone is skipped")

	h3 := http.Header{}
	h3.Set("X-Grok-Conv-Id", "019fbd1e-e7c4-7e11-98b4-a391d59ffb84")
	assert.Equal(t, "019fbd1e-e7c4-7e11-98b4-a391d59ffb84", GrokSessionIDFromHeaders(h3))
}
