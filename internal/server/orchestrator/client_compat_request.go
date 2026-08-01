package orchestrator

import (
	"context"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/pkg/xcontext"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
)

// applyGrokClientRequestCompat injects / corrects prompt_cache_key for Grok Build
// when the outbound channel is a non-xAI Responses endpoint (translate path).
//
// Key source is always the client header X-Grok-Session-Id (stable session id),
// never AH-Trace-Id (at-…) that Responses outbound may have auto-filled.
//
// Runs after pass-through / override so the final outbound body is patched.
// Marks client_compat_applied when the body is actually modified.
func applyGrokClientRequestCompat(outbound *PersistentOutboundTransformer, systemService *biz.SystemService) pipeline.Middleware {
	return pipeline.OnRawRequest("grok-client-request-compat", func(ctx context.Context, request *httpclient.Request) (*httpclient.Request, error) {
		if outbound == nil || outbound.state == nil || request == nil || len(request.Body) == 0 {
			return request, nil
		}

		key, route, ok := resolveGrokTranslateCacheKey(ctx, outbound, systemService)
		if !ok || key == "" {
			return request, nil
		}

		// Keep llm.Request in sync for rebuilds / templates / override rendering.
		if outbound.state.LlmRequest != nil {
			k := key
			outbound.state.LlmRequest.PromptCacheKey = &k
		}

		patched, changed := biz.InjectPromptCacheKeyJSON(request.Body, key)
		if !changed {
			return request, nil
		}

		request.Body = patched
		markClientCompatApplied(ctx, outbound)

		detect, _ := biz.GetClientDetect(ctx)
		profile := ""
		if detect != nil {
			profile = detect.ProfileID
		}
		log.Info(ctx, "client compat request prompt_cache_key set from Grok session header",
			log.String("profile", profile),
			log.String("route", route.Reason),
			log.String("prompt_cache_key", key),
		)

		return request, nil
	})
}

// applyGrokPromptCacheKeyBeforeTransform stamps llm.Request.PromptCacheKey from
// X-Grok-Session-Id after the channel is selected and before the provider
// TransformRequest marshals the body.
func applyGrokPromptCacheKeyBeforeTransform(
	ctx context.Context,
	llmReq *llm.Request,
	outbound *PersistentOutboundTransformer,
) *llm.Request {
	if llmReq == nil || outbound == nil {
		return llmReq
	}
	var systemService *biz.SystemService
	if outbound.state != nil {
		systemService = outbound.state.SystemService
	}
	key, _, ok := resolveGrokTranslateCacheKey(ctx, outbound, systemService)
	if !ok || key == "" {
		return llmReq
	}
	if llmReq.PromptCacheKey != nil {
		cur := strings.TrimSpace(*llmReq.PromptCacheKey)
		if cur != "" && !biz.IsAxonHubAutoPromptCacheKey(cur) && cur != key {
			return llmReq
		}
		if cur == key {
			return llmReq
		}
	}
	k := key
	llmReq.PromptCacheKey = &k
	if outbound.state != nil && outbound.state.LlmRequest != nil {
		outbound.state.LlmRequest.PromptCacheKey = &k
	}
	// Request body will carry this key → treat as a real request-side compat patch.
	markClientCompatApplied(ctx, outbound)
	return llmReq
}

func resolveGrokTranslateCacheKey(
	ctx context.Context,
	outbound *PersistentOutboundTransformer,
	systemService *biz.SystemService,
) (key string, route biz.ClientRouteDecision, ok bool) {
	detect, hasDetect := biz.GetClientDetect(ctx)
	if !hasDetect || detect == nil {
		return "", biz.ClientRouteDecision{}, false
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
	} else if outbound.state != nil && outbound.state.CurrentCandidate != nil && outbound.state.CurrentCandidate.Channel != nil {
		channelType = outbound.state.CurrentCandidate.Channel.Type.String()
	}

	inboundFormat := llm.APIFormat("")
	if outbound.state != nil && outbound.state.LlmRequest != nil {
		inboundFormat = outbound.state.LlmRequest.APIFormat
	}

	route = biz.DecideClientRoute(settings, detect, channelType, inboundFormat)
	if !route.InjectPromptCacheKey {
		return "", route, false
	}

	// Strictly from client headers — never AH-Trace-Id / context session.
	if outbound.state != nil && outbound.state.RawRequest != nil && outbound.state.RawRequest.Headers != nil {
		key = biz.GrokSessionIDFromHeaders(outbound.state.RawRequest.Headers)
	}
	if key == "" && outbound.state != nil && outbound.state.LlmRequest != nil && outbound.state.LlmRequest.RawRequest != nil {
		key = biz.GrokSessionIDFromHeaders(outbound.state.LlmRequest.RawRequest.Headers)
	}
	if key == "" {
		return "", route, false
	}
	return key, route, true
}

func markClientCompatApplied(ctx context.Context, outbound *PersistentOutboundTransformer) {
	if outbound == nil || outbound.state == nil {
		return
	}
	if outbound.state.ClientCompatApplied {
		return
	}
	outbound.state.ClientCompatApplied = true
	if outbound.state.Request != nil && outbound.state.RequestService != nil {
		persistCtx, cancel := xcontext.DetachWithTimeout(ctx, 5*time.Second)
		if err := outbound.state.RequestService.UpdateRequestClientCompatApplied(persistCtx, outbound.state.Request.ID, true); err != nil {
			log.Warn(persistCtx, "Failed to mark client_compat_applied after request inject", log.Cause(err))
		}
		cancel()
	}
}
