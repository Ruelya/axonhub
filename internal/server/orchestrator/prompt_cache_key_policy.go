package orchestrator

import (
	"context"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

// applyPromptCacheKeyPolicy stamps whether Responses outbound may derive
// prompt_cache_key from a stable client session/trace id.
//
// Default (when the system key is unset) is enabled: inject only when a
// meaningful session exists (not AxonHub auto at- ids). When disabled, never
// auto-inject; client-provided prompt_cache_key is always preserved.
func applyPromptCacheKeyPolicy(systemService *biz.SystemService) pipeline.Middleware {
	return &promptCacheKeyPolicyMiddleware{systemService: systemService}
}

type promptCacheKeyPolicyMiddleware struct {
	pipeline.DummyMiddleware

	systemService *biz.SystemService
}

func (m *promptCacheKeyPolicyMiddleware) Name() string {
	return "prompt-cache-key-policy"
}

func (m *promptCacheKeyPolicyMiddleware) OnInboundLlmRequest(ctx context.Context, request *llm.Request) (*llm.Request, error) {
	if request == nil {
		return request, nil
	}

	enabled := true
	if m.systemService != nil {
		v, err := m.systemService.AutoPromptCacheKeyFromSession(ctx)
		if err != nil {
			log.Debug(ctx, "failed to load auto prompt_cache_key policy, defaulting to enabled", log.Cause(err))
		} else {
			enabled = v
		}
	}

	if request.TransformerMetadata == nil {
		request.TransformerMetadata = map[string]any{}
	}
	request.TransformerMetadata[shared.TransformerMetadataKeyAutoPromptCacheKeyFromSession] = enabled

	return request, nil
}
