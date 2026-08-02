package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-contrib/sse"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

const (
	errTypeQuotaExhausted = "quota_exhausted"
	errCodeQuotaExhausted = "quota_exhausted"
)

// StreamWriter is a function type for writing stream events to the response.
type StreamWriter func(c *gin.Context, stream streams.Stream[*httpclient.StreamEvent])

type ChatCompletionHandlers struct {
	ChatCompletionOrchestrator *orchestrator.ChatCompletionOrchestrator
	// StreamWriter, when non-nil, replaces the default SSE writer. Custom writers
	// (Gemini, binary audio, AI SDK) skip early-SSE keepalives because they use
	// different wire framing. Nil means standard WriteSSEStream + keepalives.
	StreamWriter StreamWriter
}

func NewChatCompletionHandlers(orchestrator *orchestrator.ChatCompletionOrchestrator) *ChatCompletionHandlers {
	return &ChatCompletionHandlers{
		ChatCompletionOrchestrator: orchestrator,
	}
}

// WithStreamWriter returns a new ChatCompletionHandlers with the specified stream writer.
func (handlers *ChatCompletionHandlers) WithStreamWriter(writer StreamWriter) *ChatCompletionHandlers {
	return &ChatCompletionHandlers{
		ChatCompletionOrchestrator: handlers.ChatCompletionOrchestrator,
		StreamWriter:               writer,
	}
}

func (handlers *ChatCompletionHandlers) ChatCompletion(c *gin.Context) {
	ctx := c.Request.Context()

	// Use ReadHTTPRequest to parse the request
	genericReq, err := httpclient.ReadHTTPRequest(c.Request)
	if err != nil {
		httpErr := handlers.ChatCompletionOrchestrator.Inbound.TransformError(ctx, err)
		c.JSON(httpErr.StatusCode, json.RawMessage(httpErr.Body))

		return
	}

	handlers.ChatCompletionWithRequest(c, genericReq)
}

func (handlers *ChatCompletionHandlers) ChatCompletionWithRequest(c *gin.Context, genericReq *httpclient.Request) {
	ctx := c.Request.Context()

	if genericReq == nil || len(genericReq.Body) == 0 {
		JSONError(c, http.StatusBadRequest, errors.New("Request body is empty"))
		return
	}

	// log.Debug(ctx, "Chat completion request", log.Any("request", genericReq))

	// Prefer WebUI / system retry-policy setting; fall back to process config.
	keepAlive := effectiveSSEKeepAliveInterval(ctx, handlers.ChatCompletionOrchestrator)
	// When the client asked for a stream and keepalives are enabled, commit SSE
	// headers immediately and ping the client while Process waits on upstream.
	// This prevents Cloudflare (~100s idle) from 524'ing slow first-token cases.
	// Only for the standard SSE writer path (StreamWriter == nil). Custom writers
	// (Gemini binary, AI SDK JSON, etc.) own their own framing.
	earlySSE := keepAlive > 0 &&
		requestLikelyWantsStream(genericReq.Body) &&
		handlers.StreamWriter == nil
	headersCommitted := false

	var (
		result orchestrator.ChatCompletionResult
		err    error
	)

	if earlySSE {
		beginSSEResponse(c)
		headersCommitted = true

		result, err = waitProcessWithSSEKeepAlive(ctx, c, keepAlive, func(pctx context.Context) (orchestrator.ChatCompletionResult, error) {
			return handlers.ChatCompletionOrchestrator.Process(pctx, genericReq)
		})
	} else {
		result, err = handlers.ChatCompletionOrchestrator.Process(ctx, genericReq)
	}

	if err != nil {
		log.Error(ctx, "Error processing chat completion", log.Cause(err))

		httpErr := transformOrchestratorError(ctx, err, handlers.ChatCompletionOrchestrator)

		if headersCommitted {
			// Response status is already 200 SSE; surface the failure as an SSE error event.
			writeSSEErrorEvent(c, FormatStreamError, &httpclient.Error{
				StatusCode: httpErr.StatusCode,
				Status:     http.StatusText(httpErr.StatusCode),
				Body:       httpErr.Body,
			})

			return
		}

		c.JSON(httpErr.StatusCode, json.RawMessage(httpErr.Body))

		return
	}

	if result.ChatCompletion != nil {
		resp := result.ChatCompletion

		if headersCommitted {
			// Unexpected: body said stream but Process returned a non-stream body.
			// Headers are already SSE; emit payload as a single data event best-effort.
			log.Warn(ctx, "stream request returned non-stream response after SSE headers committed")
			c.SSEvent("", json.RawMessage(resp.Body))
			c.Writer.Flush()

			return
		}

		contentType := "application/json"
		if ct := resp.Headers.Get("Content-Type"); ct != "" {
			contentType = ct
		}

		c.Data(resp.StatusCode, contentType, resp.Body)

		return
	}

	if result.ChatCompletionStream != nil {
		defer func() {
			log.Debug(ctx, "Close chat stream")

			err := result.ChatCompletionStream.Close()
			if err != nil {
				logger.Error(ctx, "Error closing stream", log.Cause(err))
			}
		}()

		stream := newUpstreamErrorStream(ctx, result.ChatCompletionStream, handlers.ChatCompletionOrchestrator.SystemService)

		// Custom stream writers (e.g. Gemini, AI SDK, binary speech) own framing.
		// Note: WriteSSEStream itself still applies mid-stream keepalives via the global interval.
		if handlers.StreamWriter != nil {
			handlers.StreamWriter(c, stream)

			return
		}

		WriteSSEStreamWithOptions(c, stream, FormatStreamError, SSEStreamOptions{
			HeadersAlreadyWritten: headersCommitted,
			KeepAliveInterval:     keepAlive,
		})
	}
}

// StreamErrorFormatter formats a stream error into a JSON-serializable object for SSE error events.
type StreamErrorFormatter func(ctx context.Context, err error) any

// maxStreamEventsAfterCancel bounds how many events the stream writers drain after
// the request context is canceled. Draining lets persistence wrappers observe a
// buffered terminal event, but streams are expected to end promptly on cancellation
// (see passThroughChannelStream.Next); the cap only guards against implementations
// that ignore it. Pass-through channel buffers hold 64 events, so 256 is generous.
const maxStreamEventsAfterCancel = 256

// SSEStreamOptions controls WriteSSEStreamWithOptions behavior.
type SSEStreamOptions struct {
	// HeadersAlreadyWritten skips writing SSE headers (early keepalive session).
	HeadersAlreadyWritten bool
	// KeepAliveInterval sends SSE comment pings while blocked on stream.Next.
	// Zero uses the process-wide SSEKeepAliveInterval(); negative disables.
	KeepAliveInterval time.Duration
}

// WriteSSEStream writes stream events as Server-Sent Events (SSE) with default error formatting.
func WriteSSEStream(c *gin.Context, stream streams.Stream[*httpclient.StreamEvent]) {
	WriteSSEStreamWithErrorFormatter(c, stream, FormatStreamError)
}

// WriteSSEStreamWithErrorFormatter writes stream events as SSE with a custom error formatter.
func WriteSSEStreamWithErrorFormatter(c *gin.Context, stream streams.Stream[*httpclient.StreamEvent], formatErr StreamErrorFormatter) {
	WriteSSEStreamWithOptions(c, stream, formatErr, SSEStreamOptions{
		KeepAliveInterval: SSEKeepAliveInterval(),
	})
}

// WriteSSEStreamWithOptions writes stream events as SSE with optional early headers and keepalives.
func WriteSSEStreamWithOptions(c *gin.Context, stream streams.Stream[*httpclient.StreamEvent], formatErr StreamErrorFormatter, opts SSEStreamOptions) {
	ctx := c.Request.Context()
	clientDisconnected := false

	if formatErr == nil {
		formatErr = FormatStreamError
	}

	keepAlive := opts.KeepAliveInterval
	if keepAlive < 0 {
		keepAlive = 0
	}

	defer func() {
		if clientDisconnected {
			log.Warn(ctx, "Client disconnected")
		}
	}()

	if !opts.HeadersAlreadyWritten {
		// Set SSE headers
		c.Header("Content-Type", sse.ContentType)
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		c.Writer.Flush()
	}

	// Do not pre-check ctx.Done() before Next(). If the client disconnects right
	// after receiving the terminal event, a preferential ctx.Done() check can abort
	// before Next() drains EOF / the last buffered chunk, causing Close() to mark the
	// request canceled even though the stream completed. This relies on the stream
	// contract that Next() returns false promptly once cancellation is observed and
	// its buffer is drained; eventsAfterCancel bounds streams that violate it.
	eventsAfterCancel := 0

	for {
		hasNext, nextErr := nextStreamEventWithKeepAlive(ctx, c, stream, keepAlive)
		if nextErr != nil {
			clientDisconnected = true
			log.Warn(ctx, "Stream wait interrupted", log.Cause(nextErr))

			return
		}

		if !hasNext {
			if err := stream.Err(); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
					clientDisconnected = true

					// Keep genuine upstream failures visible even when the client is gone.
					if !errors.Is(err, context.Canceled) {
						log.Warn(ctx, "Stream error after client disconnected", log.Cause(err))
					}
				} else {
					log.Error(ctx, "Error in stream", log.Cause(err))
					c.SSEvent("error", formatErr(ctx, err))
				}
			} else if errors.Is(ctx.Err(), context.Canceled) {
				clientDisconnected = true
			}

			c.Writer.Flush()

			return
		}

		if ctx.Err() != nil {
			eventsAfterCancel++
			if eventsAfterCancel > maxStreamEventsAfterCancel {
				clientDisconnected = true

				log.Warn(ctx, "Stream still producing after cancellation, aborting drain",
					log.Int("events_after_cancel", eventsAfterCancel))

				return
			}
		}

		cur := stream.Current()
		c.SSEvent(cur.Type, cur.Data)
		log.Debug(ctx, "write stream event", log.Any("event", cur))
		c.Writer.Flush()
	}
}

// WriteBinaryStream writes raw bytes from stream events directly to the response body.
// The first chunk type is treated as the stream Content-Type when present.
func WriteBinaryStream(c *gin.Context, stream streams.Stream[*httpclient.StreamEvent]) {
	ctx := c.Request.Context()
	clientDisconnected := false
	headersWritten := false
	contentType := "application/octet-stream"

	defer func() {
		if clientDisconnected {
			log.Warn(ctx, "Client disconnected")
		}
	}()

	// Same as WriteSSEStream: do not pre-check ctx.Done() before Next(), so a
	// disconnect right after the terminal chunk does not skip drain / completion.
	// The drain after cancellation is bounded by eventsAfterCancel.
	eventsAfterCancel := 0

	for {
		if !stream.Next() {
			if err := stream.Err(); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
					clientDisconnected = true

					// Keep genuine upstream failures visible even when the client is gone.
					if !errors.Is(err, context.Canceled) {
						log.Warn(ctx, "Binary stream error after client disconnected", log.Cause(err))
					}
				} else {
					log.Error(ctx, "Error in binary stream", log.Cause(err))
					if !headersWritten {
						c.JSON(streamErrorStatus(err), FormatStreamError(ctx, err))
						return
					}
				}
			} else if errors.Is(ctx.Err(), context.Canceled) {
				clientDisconnected = true
			}

			c.Writer.Flush()

			return
		}

		if ctx.Err() != nil {
			eventsAfterCancel++
			if eventsAfterCancel > maxStreamEventsAfterCancel {
				clientDisconnected = true

				log.Warn(ctx, "Binary stream still producing after cancellation, aborting drain",
					log.Int("events_after_cancel", eventsAfterCancel))

				return
			}
		}

		cur := stream.Current()
		if cur != nil && cur.Type == httpclient.BinaryStreamDoneEventType {
			continue
		}

		if cur == nil || len(cur.Data) == 0 {
			continue
		}

		if !headersWritten {
			if ct := strings.TrimSpace(cur.Type); ct != "" {
				contentType = ct
			}

			c.Header("Content-Type", contentType)
			c.Header("Cache-Control", "no-cache")
			c.Header("Connection", "keep-alive")
			c.Header("Access-Control-Allow-Origin", "*")
			headersWritten = true
		}

		if _, err := c.Writer.Write(cur.Data); err != nil {
			clientDisconnected = true
			log.Warn(ctx, "Failed to write binary stream chunk", log.Cause(err))

			return
		}

		c.Writer.Flush()
	}
}

func streamErrorStatus(err error) int {
	var quotaErr *orchestrator.QuotaExhaustedError
	if errors.As(err, &quotaErr) {
		return http.StatusServiceUnavailable
	}

	var respErr *llm.ResponseError
	if errors.As(err, &respErr) && respErr.StatusCode != 0 {
		return respErr.StatusCode
	}

	var httpErr *httpclient.Error
	if errors.As(err, &httpErr) && httpErr.StatusCode != 0 {
		return httpErr.StatusCode
	}

	return http.StatusInternalServerError
}

// FormatStreamError formats a stream error into an OpenAI-compatible JSON error object.
func FormatStreamError(_ context.Context, err error) any {
	errType := "server_error"
	errCode := ""
	requestID := ""

	var quotaErr *orchestrator.QuotaExhaustedError
	if errors.As(err, &quotaErr) {
		return gin.H{
			"error": gin.H{
				"message": quotaErr.Error(),
				"type":    errTypeQuotaExhausted,
				"code":    errCodeQuotaExhausted,
			},
		}
	}

	var respErr *llm.ResponseError
	if errors.As(err, &respErr) {
		if respErr.Detail.Type != "" {
			errType = respErr.Detail.Type
		}

		errCode = respErr.Detail.Code
		requestID = respErr.Detail.RequestID

		return gin.H{
			"error": gin.H{
				"message": respErr.Detail.Message,
				"type":    errType,
				"code":    errCode,
			},
			"request_id": requestID,
		}
	}

	var httpErr *httpclient.Error
	if errors.As(err, &httpErr) && len(httpErr.Body) > 0 {
		if t := gjson.GetBytes(httpErr.Body, "error.type"); t.Exists() && t.Type == gjson.String && t.String() != "" {
			errType = t.String()
		}

		if c := gjson.GetBytes(httpErr.Body, "error.code"); c.Exists() && c.Type == gjson.String && c.String() != "" {
			errCode = c.String()
		}

		if rid := gjson.GetBytes(httpErr.Body, "request_id"); rid.Exists() && rid.Type == gjson.String && rid.String() != "" {
			requestID = rid.String()
		}
	}

	return gin.H{
		"error": gin.H{
			"message": orchestrator.ExtractErrorMessage(err),
			"type":    errType,
			"code":    errCode,
		},
		"request_id": requestID,
	}
}

func wrapQuotaExhaustedAsResponseError(err error) error {
	if err == nil {
		return nil
	}

	var quotaErr *orchestrator.QuotaExhaustedError
	if errors.As(err, &quotaErr) {
		return &llm.ResponseError{
			StatusCode: http.StatusServiceUnavailable,
			Detail: llm.ErrorDetail{
				Message: quotaErr.Error(),
				Type:    errTypeQuotaExhausted,
				Code:    errCodeQuotaExhausted,
			},
		}
	}

	return err
}
