package shared

import "strings"

// TransformerMetadataKeyAutoPromptCacheKeyFromSession controls whether the
// Responses outbound transformer may derive prompt_cache_key from a stable
// client session/trace id when the request body omits it.
// Stored on llm.Request.TransformerMetadata by the orchestrator.
const TransformerMetadataKeyAutoPromptCacheKeyFromSession = "auto_prompt_cache_key_from_session"

// IsAxonHubAutoTraceID reports whether id looks like an AxonHub-generated
// per-request trace id (GenerateTraceID → "at-<uuid>"), optionally with a
// conversation-anchor suffix ("at-<uuid>-<16hex>").
//
// These ids change every request when the client sends no session/trace header,
// so they are useless as prompt_cache_key for multi-request caching.
func IsAxonHubAutoTraceID(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(id), "at-")
}

// AutoPromptCacheKeyFromSessionEnabled reads the orchestrator flag from
// transformer metadata. Default is true when unset.
func AutoPromptCacheKeyFromSessionEnabled(meta map[string]any) bool {
	if meta == nil {
		return true
	}
	v, ok := meta[TransformerMetadataKeyAutoPromptCacheKeyFromSession]
	if !ok {
		return true
	}
	enabled, ok := v.(bool)
	if !ok {
		return true
	}
	return enabled
}
