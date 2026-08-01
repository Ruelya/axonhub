package biz

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/llm"
)

// ClientRouteDecision describes request/response compat actions for a
// detected client routed to a specific outbound channel.
//
// Grok Build + xAI Responses channel: match → pass-through (no request/response patches).
// Grok Build + any other Responses-family channel: translate → inject prompt_cache_key
// on the request and normalize response annotations/id/status on the wire.
type ClientRouteDecision struct {
	// InjectPromptCacheKey adds prompt_cache_key when missing on the outbound request body.
	InjectPromptCacheKey bool
	// ResponsePatches are wire patches for the client-facing response.
	ResponsePatches ClientPatchConfig
	// ResponsePatchActive is true when response wire patching should run.
	ResponsePatchActive bool
	// Reason is a short debug label (passthrough_xai | translate_responses | profile | none).
	Reason string
}

// IsXAIResponsesChannel reports whether the channel type is native xAI Responses.
func IsXAIResponsesChannel(channelType string) bool {
	return strings.EqualFold(strings.TrimSpace(channelType), string(channel.TypeXaiResponses))
}

// IsResponsesFamilyChannel reports whether the channel's primary surface is OpenAI Responses.
func IsResponsesFamilyChannel(channelType string) bool {
	t := channel.Type(strings.TrimSpace(channelType))
	switch t {
	case channel.TypeOpenaiResponses,
		channel.TypeCodex,
		channel.TypeXaiResponses,
		channel.TypeNanogptResponses:
		return true
	default:
		// Fall back to default endpoint mapping when type is unknown here.
		return false
	}
}

// IsResponsesAPIFormat reports whether the inbound/outbound API format is Responses.
func IsResponsesAPIFormat(format llm.APIFormat) bool {
	switch format {
	case llm.APIFormatOpenAIResponse, llm.APIFormatOpenAIResponseCompact:
		return true
	default:
		s := strings.ToLower(string(format))
		return strings.Contains(s, "response")
	}
}

// DecideClientRoute chooses request/response compat actions for the detect result
// and selected outbound channel.
func DecideClientRoute(
	settings *ClientCompatSettings,
	detect *ClientDetectResult,
	channelType string,
	inboundFormat llm.APIFormat,
) ClientRouteDecision {
	if settings == nil || detect == nil || !settings.Enabled {
		return ClientRouteDecision{Reason: "none"}
	}
	profile, ok := settings.Profiles[detect.ProfileID]
	if !ok || !profile.Enabled || !profile.Patches.AnyActive() {
		return ClientRouteDecision{Reason: "none"}
	}

	patches := profile.Patches
	if patches.EnsureOutputTextAnnotations && !patches.EnsureStrictResponsesOutput {
		patches.EnsureStrictResponsesOutput = true
	}

	// Grok Build: match xAI Responses channel → pass-through; other Responses channels → translate.
	if detect.ProfileID == ClientProfileGrokBuild {
		if IsXAIResponsesChannel(channelType) {
			return ClientRouteDecision{Reason: "passthrough_xai"}
		}
		// Non-xAI Responses endpoints (openai_responses, codex/Shuai, …): bidirectional translate.
		if IsResponsesFamilyChannel(channelType) {
			return ClientRouteDecision{
				InjectPromptCacheKey: true,
				ResponsePatches:      patches,
				ResponsePatchActive:  true,
				Reason:               "translate_responses",
			}
		}
		// Chat/messages channels: no Responses wire translate.
		_ = inboundFormat
		return ClientRouteDecision{Reason: "none"}
	}

	// Other profiles keep the legacy profile-only response patch behavior.
	return ClientRouteDecision{
		ResponsePatches:     patches,
		ResponsePatchActive: true,
		Reason:              "profile",
	}
}

// GrokSessionIDFromHeaders extracts a stable session key for prompt_cache_key injection.
// Prefers X-Grok-Session-Id; falls back to non-recap conv id / Session-Id.
func GrokSessionIDFromHeaders(headers http.Header) string {
	if headers == nil {
		return ""
	}
	candidates := []string{
		headers.Get("X-Grok-Session-Id"),
		headers.Get("x-grok-session-id"),
		headers.Get("X-Grok-Conv-Id"),
		headers.Get("x-grok-conv-id"),
		headers.Get("Session-Id"),
		headers.Get("Session_id"),
		headers.Get("session-id"),
	}
	for _, raw := range candidates {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		// Skip synthetic recap conversation ids for cache key stability.
		if strings.HasPrefix(strings.ToLower(id), "recap-") {
			continue
		}
		return id
	}
	return ""
}

// GrokSessionIDFromHeaderMap works with map-style stored headers (JSON from DB).
func GrokSessionIDFromHeaderMap(headers map[string]any) string {
	if headers == nil {
		return ""
	}
	get := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := headers[k]; ok {
				switch t := v.(type) {
				case string:
					if s := strings.TrimSpace(t); s != "" {
						return s
					}
				case []any:
					if len(t) > 0 {
						if s, ok := t[0].(string); ok {
							if s = strings.TrimSpace(s); s != "" {
								return s
							}
						}
					}
				case []string:
					if len(t) > 0 {
						if s := strings.TrimSpace(t[0]); s != "" {
							return s
						}
					}
				}
			}
		}
		return ""
	}
	for _, id := range []string{
		get("X-Grok-Session-Id", "x-grok-session-id"),
		get("X-Grok-Conv-Id", "x-grok-conv-id"),
		get("Session-Id", "Session_id", "session-id"),
	} {
		if id == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(id), "recap-") {
			continue
		}
		return id
	}
	return ""
}

// IsAxonHubAutoPromptCacheKey reports whether key looks like AxonHub's fallback
// (AH-Trace-Id "at-<uuid>" or "at-<uuid>-<anchor>"), not a client session id.
func IsAxonHubAutoPromptCacheKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	// GenerateTraceID format: at-{{uuid}} optionally + "-" + conversation anchor.
	return strings.HasPrefix(strings.ToLower(key), "at-")
}

// InjectPromptCacheKeyJSON sets prompt_cache_key on a JSON request body.
// - If missing/empty → set to key.
// - If existing is an AxonHub auto key (at-…) → replace with key.
// - If existing already equals key → no change.
// - If existing is another non-empty client key → leave as-is (respect client/recap).
// Returns (body, true) when the body was modified.
func InjectPromptCacheKeyJSON(body []byte, key string) ([]byte, bool) {
	key = strings.TrimSpace(key)
	if key == "" || len(body) == 0 {
		return body, false
	}
	trim := strings.TrimSpace(string(body))
	if trim == "" || trim[0] != '{' {
		return body, false
	}
	existing := strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String())
	if existing == key {
		return body, false
	}
	// Respect an explicit client-provided key (e.g. recap already set session id),
	// but always overwrite AxonHub auto keys so Grok session id wins.
	if existing != "" && !IsAxonHubAutoPromptCacheKey(existing) {
		return body, false
	}
	out, err := sjson.SetBytes(body, "prompt_cache_key", key)
	if err != nil {
		return body, false
	}
	return out, true
}

// PreviewPromptCacheKeyPatch is a dry-run helper for UI / tests.
func PreviewPromptCacheKeyPatch(body []byte, key string) (patched []byte, changed bool) {
	return InjectPromptCacheKeyJSON(body, key)
}

// HasPromptCacheKey reports whether body already carries a non-empty prompt_cache_key.
func HasPromptCacheKey(body []byte) bool {
	return strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String()) != ""
}

// ResolveGrokPromptCacheKey returns the stable cache key for Grok Build from headers.
// Prefer X-Grok-Session-Id; never use AH-Trace-Id.
func ResolveGrokPromptCacheKey(headers http.Header) string {
	return GrokSessionIDFromHeaders(headers)
}
