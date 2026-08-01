package orchestrator

import (
	"context"
	"time"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/pkg/xcontext"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
)

// applyGrokClientRequestCompat injects prompt_cache_key for Grok Build when the
// outbound channel is a non-xAI Responses endpoint (translate path).
// Runs after pass-through / override so the final outbound body is patched.
// Marks client_compat_applied only when the body is actually modified.
func applyGrokClientRequestCompat(outbound *PersistentOutboundTransformer, systemService *biz.SystemService) pipeline.Middleware {
	return pipeline.OnRawRequest("grok-client-request-compat", func(ctx context.Context, request *httpclient.Request) (*httpclient.Request, error) {
		if outbound == nil || outbound.state == nil || request == nil || len(request.Body) == 0 {
			return request, nil
		}

		detect, ok := biz.GetClientDetect(ctx)
		if !ok || detect == nil {
			return request, nil
		}

		var settings *biz.ClientCompatSettings
		if systemService != nil {
			settings = systemService.ClientCompatSettingsOrDefault(ctx)
		} else {
			def := biz.DefaultClientCompatSettings()
			settings = &def
		}

		channelType := ""
		if ch := outbound.GetCurrentChannel(); ch != nil {
			channelType = ch.Type.String()
		}

		inboundFormat := llm.APIFormat("")
		if outbound.state.LlmRequest != nil {
			inboundFormat = outbound.state.LlmRequest.APIFormat
		}

		route := biz.DecideClientRoute(settings, detect, channelType, inboundFormat)
		if !route.InjectPromptCacheKey {
			return request, nil
		}

		// Prefer session from the original client headers (Grok X-Grok-Session-Id).
		key := ""
		if outbound.state.RawRequest != nil && outbound.state.RawRequest.Headers != nil {
			key = biz.GrokSessionIDFromHeaders(outbound.state.RawRequest.Headers)
		}
		if key == "" && outbound.state.LlmRequest != nil && outbound.state.LlmRequest.RawRequest != nil {
			key = biz.GrokSessionIDFromHeaders(outbound.state.LlmRequest.RawRequest.Headers)
		}
		if key == "" {
			// Last resort: structured field already populated upstream.
			if outbound.state.LlmRequest != nil && outbound.state.LlmRequest.PromptCacheKey != nil {
				key = *outbound.state.LlmRequest.PromptCacheKey
			}
		}
		if key == "" {
			return request, nil
		}

		// Keep llm.Request in sync for non-pass-through rebuilds / templates.
		if outbound.state.LlmRequest != nil {
			if outbound.state.LlmRequest.PromptCacheKey == nil || *outbound.state.LlmRequest.PromptCacheKey == "" {
				k := key
				outbound.state.LlmRequest.PromptCacheKey = &k
			}
		}

		patched, changed := biz.InjectPromptCacheKeyJSON(request.Body, key)
		if !changed {
			return request, nil
		}

		request.Body = patched
		if !outbound.state.ClientCompatApplied {
			outbound.state.ClientCompatApplied = true
			if outbound.state.Request != nil && outbound.state.RequestService != nil {
				persistCtx, cancel := xcontext.DetachWithTimeout(ctx, 5*time.Second)
				if err := outbound.state.RequestService.UpdateRequestClientCompatApplied(persistCtx, outbound.state.Request.ID, true); err != nil {
					log.Warn(persistCtx, "Failed to mark client_compat_applied after request inject", log.Cause(err))
				}
				cancel()
			}
		}

		log.Debug(ctx, "client compat request prompt_cache_key injected",
			log.String("profile", detect.ProfileID),
			log.String("route", route.Reason),
			log.String("channel_type", channelType),
			log.String("prompt_cache_key", key),
		)

		return request, nil
	})
}
