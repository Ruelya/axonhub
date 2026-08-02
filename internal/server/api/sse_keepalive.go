package api

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gin-contrib/sse"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

// sseKeepAliveIntervalNs holds the configured SSE keepalive interval in nanoseconds.
// 0 disables keepalives. Updated at server start via SetSSEKeepAliveInterval.
var sseKeepAliveIntervalNs atomic.Int64

// SetSSEKeepAliveInterval configures how often AxonHub writes SSE comment keepalives
// to the downstream client. Zero disables the feature.
func SetSSEKeepAliveInterval(d time.Duration) {
	if d < 0 {
		d = 0
	}

	sseKeepAliveIntervalNs.Store(int64(d))
}

// SSEKeepAliveInterval returns the process-level keepalive interval from config/env (0 = disabled).
func SSEKeepAliveInterval() time.Duration {
	return time.Duration(sseKeepAliveIntervalNs.Load())
}

// effectiveSSEKeepAliveInterval prefers the WebUI/system RetryPolicy value when available,
// otherwise falls back to the process-level config (server.sse_keepalive_interval).
func effectiveSSEKeepAliveInterval(ctx context.Context, orch *orchestrator.ChatCompletionOrchestrator) time.Duration {
	if orch != nil && orch.SystemService != nil {
		policy := orch.SystemService.RetryPolicyOrDefault(ctx)
		if policy != nil {
			return time.Duration(policy.SSEKeepAliveIntervalSeconds) * time.Second
		}
	}

	return SSEKeepAliveInterval()
}

// requestLikelyWantsStream reports whether the inbound body indicates a streaming
// LLM request. Used to start SSE headers/keepalives before orchestrator.Process returns.
func requestLikelyWantsStream(body []byte) bool {
	if len(body) == 0 {
		return false
	}

	streamField := gjson.GetBytes(body, "stream")
	if streamField.Exists() {
		return streamField.Bool()
	}

	// stream_options is only meaningful with streaming OpenAI chat completions.
	if gjson.GetBytes(body, "stream_options").Exists() {
		return true
	}

	return false
}

// beginSSEResponse writes SSE response headers and flushes so intermediate proxies
// observe an open streaming response (critical for Cloudflare idle limits).
func beginSSEResponse(c *gin.Context) {
	c.Header("Content-Type", sse.ContentType)
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	// Discourage reverse proxies from buffering the entire stream.
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	c.Writer.Flush()
}

// writeSSEKeepAliveComment writes an SSE comment (ignored by clients) and flushes.
// Comments keep the connection active without being parsed as model output.
func writeSSEKeepAliveComment(c *gin.Context) error {
	// Leading ":" is the SSE comment form; blank line ends the event.
	if _, err := fmt.Fprintf(c.Writer, ": keepalive %d\n\n", time.Now().Unix()); err != nil {
		return err
	}

	c.Writer.Flush()

	return nil
}

// writeSSEErrorEvent emits a terminal SSE error event after headers are already committed.
func writeSSEErrorEvent(c *gin.Context, formatErr StreamErrorFormatter, err error) {
	if formatErr == nil {
		formatErr = FormatStreamError
	}

	c.SSEvent("error", formatErr(c.Request.Context(), err))
	c.Writer.Flush()
}

type processOutcome struct {
	result orchestrator.ChatCompletionResult
	err    error
}

// waitProcessWithSSEKeepAlive runs Process in a background goroutine while the
// request goroutine writes SSE comment keepalives until Process returns.
func waitProcessWithSSEKeepAlive(
	ctx context.Context,
	c *gin.Context,
	interval time.Duration,
	process func(context.Context) (orchestrator.ChatCompletionResult, error),
) (orchestrator.ChatCompletionResult, error) {
	if interval <= 0 {
		return process(ctx)
	}

	ch := make(chan processOutcome, 1)

	go func() {
		result, err := process(ctx)
		ch <- processOutcome{result: result, err: err}
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Immediate comment so TTFB is near-zero after headers (helps strict proxies).
	if err := writeSSEKeepAliveComment(c); err != nil {
		log.Warn(ctx, "SSE keepalive write failed (client gone?)", log.Cause(err))

		select {
		case out := <-ch:
			if out.err != nil {
				return out.result, out.err
			}

			return out.result, err
		case <-ctx.Done():
			out := <-ch

			return out.result, ctx.Err()
		}
	}

	for {
		select {
		case out := <-ch:
			return out.result, out.err
		case <-ticker.C:
			if err := writeSSEKeepAliveComment(c); err != nil {
				log.Warn(ctx, "SSE keepalive write failed (client gone?)", log.Cause(err))

				select {
				case out := <-ch:
					if out.err != nil {
						return out.result, out.err
					}

					return out.result, err
				case <-ctx.Done():
					out := <-ch

					return out.result, ctx.Err()
				}
			}
		case <-ctx.Done():
			out := <-ch

			return out.result, ctx.Err()
		}
	}
}

// nextStreamEventWithKeepAlive blocks on stream.Next while periodically writing
// SSE keepalives. Stream.Next must not be called concurrently; only one call is
// in flight at a time.
func nextStreamEventWithKeepAlive(
	ctx context.Context,
	c *gin.Context,
	stream streams.Stream[*httpclient.StreamEvent],
	interval time.Duration,
) (bool, error) {
	if interval <= 0 {
		return stream.Next(), nil
	}

	type nextResult struct {
		ok bool
	}

	ch := make(chan nextResult, 1)

	go func() {
		ch <- nextResult{ok: stream.Next()}
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case r := <-ch:
			return r.ok, nil
		case <-ticker.C:
			if err := writeSSEKeepAliveComment(c); err != nil {
				// Client disconnected while waiting for the next upstream event.
				select {
				case r := <-ch:
					return r.ok, nil
				default:
					return false, err
				}
			}
		case <-ctx.Done():
			// Let Next observe cancellation and finish; avoids leaking the goroutine
			// when the stream respects context cancel.
			select {
			case r := <-ch:
				return r.ok, nil
			case <-time.After(5 * time.Second):
				return false, ctx.Err()
			}
		}
	}
}
