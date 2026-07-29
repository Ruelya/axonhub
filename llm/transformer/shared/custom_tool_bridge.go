package shared

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/looplj/axonhub/llm"
)

// Metadata keys stored on llm.Request.TransformerMetadata when the bridge runs.
const (
	MetaCustomToolBridgeEnabled = "custom_tool_bridge_enabled"
	MetaCustomToolBridgeNames   = "custom_tool_bridge_names"
)

// ChannelTypeXaiResponses is the AxonHub channel type that talks to xAI via
// OpenAI Responses API and enables Codex freeform↔function tool bridging.
const ChannelTypeXaiResponses = "xai_responses"

// DefaultBridgedCustomToolNames are Responses freeform tools that xAI does not
// accept as type=custom. Only these names are converted; shell_command / MCP
// function tools stay untouched.
var DefaultBridgedCustomToolNames = map[string]struct{}{
	"apply_patch": {},
}

// BridgeDecision describes whether / how custom tools should be bridged for an
// outbound attempt.
type BridgeDecision struct {
	Enabled bool
	// Names is the set of custom tool names to bridge (matched as emitted by Codex).
	Names map[string]struct{}
}

// ShouldBridgeCustomTools is true only when:
//  1. the selected outbound channel type is xai_responses, and
//  2. the inbound client request is OpenAI Responses (Codex / openai_responses).
//
// Ordinary xai (chat completions) channels and OpenAI Responses→OpenAI Responses
// routes are not bridged.
func ShouldBridgeCustomTools(channelType string, inboundFormat llm.APIFormat) bool {
	if !strings.EqualFold(strings.TrimSpace(channelType), ChannelTypeXaiResponses) {
		return false
	}
	return inboundFormat == llm.APIFormatOpenAIResponse || inboundFormat == llm.APIFormatOpenAIResponseCompact
}

// NewBridgeDecision builds a decision for the given outbound channel and inbound
// client API format.
func NewBridgeDecision(channelType string, inboundFormat llm.APIFormat) BridgeDecision {
	if !ShouldBridgeCustomTools(channelType, inboundFormat) {
		return BridgeDecision{Enabled: false}
	}
	names := make(map[string]struct{}, len(DefaultBridgedCustomToolNames))
	for n := range DefaultBridgedCustomToolNames {
		names[n] = struct{}{}
	}
	return BridgeDecision{Enabled: true, Names: names}
}

// BridgeRequestForOutbound converts Responses custom tool definitions and
// history for providers that only understand standard function tools.
//
// Transformations:
//   - tools: type custom (apply_patch) → type function with {input: string}
//   - assistant tool_calls: custom_tool_call → function_call with JSON args
//   - tool results keep the same call id (function_call_output compatible)
//
// Returns a shallow-cloned request when changes are made; otherwise req.
func BridgeRequestForOutbound(req *llm.Request, decision BridgeDecision) *llm.Request {
	if req == nil || !decision.Enabled || len(decision.Names) == 0 {
		return req
	}

	// Always clone when the channel decision is on, so metadata is set for the
	// response path even if this turn has no apply_patch yet (model may still
	// return one). Also guarantees RawTools are stripped for xAI.
	cloned := *req
	cloned.Tools = bridgeTools(req.Tools, decision.Names)
	cloned.Messages = bridgeMessagesOutbound(req.Messages, decision.Names)

	// Drop raw Responses tool fragments so non-OpenAI Responses upstreams do
	// not receive unconverted custom tool JSON from ProviderExtensions.
	if cloned.ProviderExtensions != nil && cloned.ProviderExtensions.OpenAIResponses != nil &&
		cloned.ProviderExtensions.OpenAIResponses.Request != nil {
		reqExt := *cloned.ProviderExtensions.OpenAIResponses.Request
		reqExt.RawTools = nil
		reqExt.ToolSignatures = nil
		oa := *cloned.ProviderExtensions.OpenAIResponses
		oa.Request = &reqExt
		pe := *cloned.ProviderExtensions
		pe.OpenAIResponses = &oa
		cloned.ProviderExtensions = &pe
	}

	meta := cloneMetadata(req.TransformerMetadata)
	meta[MetaCustomToolBridgeEnabled] = true
	meta[MetaCustomToolBridgeNames] = bridgedNameList(decision.Names)
	cloned.TransformerMetadata = meta

	return &cloned
}

// RestoreCustomToolCallsOnResponse converts function-style apply_patch calls
// from the upstream model back into Responses custom_tool_call so Codex can
// execute them without aborting.
func RestoreCustomToolCallsOnResponse(resp *llm.Response, decision BridgeDecision) *llm.Response {
	if resp == nil || !decision.Enabled || len(decision.Names) == 0 {
		return resp
	}
	if !responseNeedsCustomRestore(resp, decision.Names) {
		return resp
	}

	cloned := *resp
	cloned.Choices = make([]llm.Choice, len(resp.Choices))
	for i, choice := range resp.Choices {
		cloned.Choices[i] = choice
		if choice.Message != nil {
			msg := *choice.Message
			msg.ToolCalls = restoreToolCalls(choice.Message.ToolCalls, decision.Names)
			cloned.Choices[i].Message = &msg
		}
		if choice.Delta != nil {
			delta := *choice.Delta
			delta.ToolCalls = restoreToolCalls(choice.Delta.ToolCalls, decision.Names)
			cloned.Choices[i].Delta = &delta
		}
	}
	return &cloned
}

// BridgeDecisionFromRequest reads bridge flags written by BridgeRequestForOutbound.
func BridgeDecisionFromRequest(req *llm.Request) BridgeDecision {
	if req == nil || req.TransformerMetadata == nil {
		return BridgeDecision{}
	}
	enabled, _ := req.TransformerMetadata[MetaCustomToolBridgeEnabled].(bool)
	if !enabled {
		return BridgeDecision{}
	}
	names := map[string]struct{}{}
	switch v := req.TransformerMetadata[MetaCustomToolBridgeNames].(type) {
	case []string:
		for _, n := range v {
			names[n] = struct{}{}
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				names[s] = struct{}{}
			}
		}
	}
	if len(names) == 0 {
		for n := range DefaultBridgedCustomToolNames {
			names[n] = struct{}{}
		}
	}
	return BridgeDecision{Enabled: true, Names: names}
}

// --- internals ---

func requestNeedsCustomToolBridge(req *llm.Request, names map[string]struct{}) bool {
	for _, t := range req.Tools {
		if t.Type == llm.ToolTypeResponsesCustomTool && t.ResponseCustomTool != nil {
			if _, ok := names[t.ResponseCustomTool.Name]; ok {
				return true
			}
		}
	}
	for _, msg := range req.Messages {
		for _, tc := range msg.ToolCalls {
			if name, ok := customToolCallName(tc); ok {
				if _, allowed := names[name]; allowed {
					return true
				}
			}
		}
	}
	return false
}

func responseNeedsCustomRestore(resp *llm.Response, names map[string]struct{}) bool {
	for _, choice := range resp.Choices {
		if choice.Message != nil {
			for _, tc := range choice.Message.ToolCalls {
				if shouldRestoreToolCall(tc, names) {
					return true
				}
			}
		}
		if choice.Delta != nil {
			for _, tc := range choice.Delta.ToolCalls {
				if shouldRestoreToolCall(tc, names) {
					return true
				}
			}
		}
	}
	return false
}

func shouldRestoreToolCall(tc llm.ToolCall, names map[string]struct{}) bool {
	// Already custom — nothing to do.
	if tc.ResponseCustomToolCall != nil {
		return false
	}
	name := tc.Function.Name
	if name == "" {
		return false
	}
	_, ok := names[name]
	return ok
}

func bridgeTools(tools []llm.Tool, names map[string]struct{}) []llm.Tool {
	if len(tools) == 0 {
		return tools
	}
	out := make([]llm.Tool, 0, len(tools))
	for _, t := range tools {
		if t.Type == llm.ToolTypeResponsesCustomTool && t.ResponseCustomTool != nil {
			if _, ok := names[t.ResponseCustomTool.Name]; ok {
				out = append(out, customToolToFunctionTool(t.ResponseCustomTool))
				continue
			}
		}
		out = append(out, t)
	}
	return out
}

func customToolToFunctionTool(custom *llm.ResponseCustomTool) llm.Tool {
	desc := custom.Description
	if desc == "" {
		desc = "Apply a freeform patch to edit files. Pass the full patch text (*** Begin Patch ... *** End Patch) as the input string. Do not wrap the patch in additional JSON beyond the function arguments object."
	} else {
		desc = desc + "\n\nIMPORTANT: This tool is exposed as a standard function tool. Put the entire freeform patch text in the \"input\" string argument (*** Begin Patch ... *** End Patch). Do not use a freeform/custom tool envelope."
	}

	params, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"input": map[string]any{
				"type":        "string",
				"description": "Full freeform patch body for apply_patch, including *** Begin Patch and *** End Patch markers.",
			},
		},
		"required":             []string{"input"},
		"additionalProperties": false,
	})

	return llm.Tool{
		Type: llm.ToolTypeFunction,
		Function: llm.Function{
			Name:        custom.Name,
			Description: desc,
			Parameters:  params,
		},
	}
}

func bridgeMessagesOutbound(messages []llm.Message, names map[string]struct{}) []llm.Message {
	if len(messages) == 0 {
		return messages
	}
	out := make([]llm.Message, len(messages))
	for i, msg := range messages {
		out[i] = msg
		if len(msg.ToolCalls) == 0 {
			continue
		}
		out[i].ToolCalls = bridgeToolCallsOutbound(msg.ToolCalls, names)
	}
	return out
}

func bridgeToolCallsOutbound(calls []llm.ToolCall, names map[string]struct{}) []llm.ToolCall {
	out := make([]llm.ToolCall, 0, len(calls))
	for _, tc := range calls {
		name, isCustom := customToolCallName(tc)
		if !isCustom {
			out = append(out, tc)
			continue
		}
		if _, ok := names[name]; !ok {
			out = append(out, tc)
			continue
		}
		input := ""
		callID := tc.ID
		if tc.ResponseCustomToolCall != nil {
			input = tc.ResponseCustomToolCall.Input
			if tc.ResponseCustomToolCall.CallID != "" {
				callID = tc.ResponseCustomToolCall.CallID
			}
		}
		args, _ := json.Marshal(map[string]string{"input": input})
		out = append(out, llm.ToolCall{
			ID:    callID,
			Type:  llm.ToolTypeFunction,
			Index: tc.Index,
			Function: llm.FunctionCall{
				Name:      name,
				Arguments: string(args),
			},
			CacheControl:        tc.CacheControl,
			TransformerMetadata: tc.TransformerMetadata,
		})
	}
	return out
}

func restoreToolCalls(calls []llm.ToolCall, names map[string]struct{}) []llm.ToolCall {
	if len(calls) == 0 {
		return calls
	}
	out := make([]llm.ToolCall, 0, len(calls))
	for _, tc := range calls {
		if !shouldRestoreToolCall(tc, names) {
			out = append(out, tc)
			continue
		}
		input := ExtractBridgedInput(tc.Function.Arguments)
		callID := tc.ID
		if callID == "" && tc.ResponseCustomToolCall != nil {
			callID = tc.ResponseCustomToolCall.CallID
		}
		// Streaming deltas may send only a name first (empty arguments). Still
		// retype as custom so the client starts a custom_tool_call item.
		out = append(out, llm.ToolCall{
			ID:    callID,
			Type:  llm.ToolTypeResponsesCustomTool,
			Index: tc.Index,
			ResponseCustomToolCall: &llm.ResponseCustomToolCall{
				CallID: callID,
				Name:   tc.Function.Name,
				Input:  input,
			},
			// Keep Function populated for any intermediate consumers that read it.
			Function:            tc.Function,
			CacheControl:        tc.CacheControl,
			TransformerMetadata: tc.TransformerMetadata,
		})
	}
	return out
}

// ExtractBridgedInput pulls freeform patch text from function arguments.
// Accepts {"input":"..."}, {"patch":"..."}, or a bare non-JSON string.
func ExtractBridgedInput(arguments string) string {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return ""
	}

	var obj map[string]any
	if err := json.Unmarshal([]byte(arguments), &obj); err == nil {
		for _, key := range []string{"input", "patch", "content", "text"} {
			if v, ok := obj[key]; ok {
				switch s := v.(type) {
				case string:
					return s
				default:
					b, _ := json.Marshal(s)
					return string(b)
				}
			}
		}
		// Single-key object whose value is a string — use it.
		if len(obj) == 1 {
			for _, v := range obj {
				if s, ok := v.(string); ok {
					return s
				}
			}
		}
		return arguments
	}

	// Not JSON — treat entire arguments as freeform input.
	return arguments
}

func customToolCallName(tc llm.ToolCall) (string, bool) {
	if tc.ResponseCustomToolCall != nil && tc.ResponseCustomToolCall.Name != "" {
		return tc.ResponseCustomToolCall.Name, true
	}
	if tc.Type == llm.ToolTypeResponsesCustomTool && tc.Function.Name != "" {
		return tc.Function.Name, true
	}
	return "", false
}

func bridgedNameList(names map[string]struct{}) []string {
	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	return out
}

func cloneMetadata(src map[string]any) map[string]any {
	if src == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(src)+2)
	for k, v := range src {
		out[k] = v
	}
	return out
}

// responseStream is the subset of streams.Stream[*llm.Response] we need.
type responseStream interface {
	Next() bool
	Current() *llm.Response
	Err() error
	Close() error
}

// restoreCustomToolStream wraps an llm.Response stream and restores bridged
// function tool calls to custom form on every chunk. It tracks accumulated
// function arguments so partial JSON deltas are converted into freeform input
// deltas instead of dumping raw JSON fragments to Codex.
type restoreCustomToolStream struct {
	inner    responseStream
	decision BridgeDecision
	// accumArgs is call-key → concatenated function.arguments so far.
	accumArgs map[string]string
	// emittedInput is call-key → freeform input already sent to the client.
	emittedInput map[string]string
	// callNames / callIDs remember identity across argument-only deltas
	// (providers often omit name/id after the first tool_call chunk).
	callNames map[string]string
	callIDs   map[string]string
	current   *llm.Response
}

// NewRestoreCustomToolStream returns a stream that maps function apply_patch
// calls back to Responses custom_tool_call shape.
func NewRestoreCustomToolStream(inner responseStream, decision BridgeDecision) *restoreCustomToolStream {
	return &restoreCustomToolStream{
		inner:        inner,
		decision:     decision,
		accumArgs:    map[string]string{},
		emittedInput: map[string]string{},
		callNames:    map[string]string{},
		callIDs:      map[string]string{},
	}
}

func (s *restoreCustomToolStream) Next() bool {
	if !s.inner.Next() {
		s.current = nil
		return false
	}
	s.current = s.restoreStreamChunk(s.inner.Current())
	return true
}

func (s *restoreCustomToolStream) Current() *llm.Response {
	return s.current
}

func (s *restoreCustomToolStream) Err() error {
	return s.inner.Err()
}

func (s *restoreCustomToolStream) Close() error {
	return s.inner.Close()
}

func (s *restoreCustomToolStream) restoreStreamChunk(resp *llm.Response) *llm.Response {
	if resp == nil || !s.decision.Enabled {
		return resp
	}
	// Non-stream complete message path.
	if len(resp.Choices) > 0 && resp.Choices[0].Message != nil && len(resp.Choices[0].Message.ToolCalls) > 0 {
		return RestoreCustomToolCallsOnResponse(resp, s.decision)
	}

	cloned := *resp
	cloned.Choices = make([]llm.Choice, len(resp.Choices))
	for i, choice := range resp.Choices {
		cloned.Choices[i] = choice
		if choice.Delta == nil || len(choice.Delta.ToolCalls) == 0 {
			continue
		}
		delta := *choice.Delta
		delta.ToolCalls = s.restoreStreamToolCalls(choice.Delta.ToolCalls)
		cloned.Choices[i].Delta = &delta
	}
	return &cloned
}

func (s *restoreCustomToolStream) restoreStreamToolCalls(calls []llm.ToolCall) []llm.ToolCall {
	out := make([]llm.ToolCall, 0, len(calls))
	for _, tc := range calls {
		key, name, ok := s.streamRestoreIdentity(tc)
		if !ok {
			out = append(out, tc)
			continue
		}
		s.accumArgs[key] += tc.Function.Arguments

		// Prefer progressive extraction of the "input" field; fall back to empty
		// until JSON is complete enough to parse.
		fullInput := progressiveExtractInput(s.accumArgs[key])
		// If progressive parse still empty but we have a complete args blob, use it.
		if fullInput == "" {
			fullInput = ExtractBridgedInput(s.accumArgs[key])
		}
		prev := s.emittedInput[key]
		deltaInput := ""
		if strings.HasPrefix(fullInput, prev) {
			deltaInput = fullInput[len(prev):]
			s.emittedInput[key] = fullInput
		} else if fullInput != "" {
			// Extraction rewound (rare) — send full remaining text once.
			deltaInput = fullInput
			s.emittedInput[key] = fullInput
		}

		callID := s.callIDs[key]
		fn := tc.Function
		if fn.Name == "" {
			fn.Name = name
		}
		out = append(out, llm.ToolCall{
			ID:    callID,
			Type:  llm.ToolTypeResponsesCustomTool,
			Index: tc.Index,
			ResponseCustomToolCall: &llm.ResponseCustomToolCall{
				CallID: callID,
				Name:   name,
				Input:  deltaInput,
			},
			Function:            fn,
			CacheControl:        tc.CacheControl,
			TransformerMetadata: tc.TransformerMetadata,
		})
	}
	return out
}

// streamRestoreIdentity resolves a stable stream key and tool name for a delta.
// Providers commonly send name/id only on the first tool_call chunk, then bare
// argument fragments. Keys are index-based so those fragments stay associated.
func (s *restoreCustomToolStream) streamRestoreIdentity(tc llm.ToolCall) (key, name string, ok bool) {
	if tc.ResponseCustomToolCall != nil {
		// Already custom — leave to non-restore path (pass through).
		return "", "", false
	}
	key = streamCallKey(tc)
	name = strings.TrimSpace(tc.Function.Name)
	if name == "" {
		name = s.callNames[key]
	}
	if name == "" {
		return key, "", false
	}
	if _, allowed := s.decision.Names[name]; !allowed {
		return key, name, false
	}
	s.callNames[key] = name
	if tc.ID != "" {
		s.callIDs[key] = tc.ID
	}
	return key, name, true
}

// streamCallKey is stable across name-/id-less argument deltas. OpenAI-style
// streams key tool fragments by index; id/name often appear only on the first
// chunk. Never include name in the key (that split empty-input restores).
func streamCallKey(tc llm.ToolCall) string {
	return "idx:" + strconv.Itoa(tc.Index)
}

// progressiveExtractInput tries to read the freeform input from a possibly
// incomplete JSON arguments buffer. Returns "" until a usable prefix is known.
func progressiveExtractInput(accum string) string {
	accum = strings.TrimSpace(accum)
	if accum == "" {
		return ""
	}
	// Complete JSON first.
	if in := ExtractBridgedInput(accum); in != "" && (strings.HasPrefix(accum, "{") || strings.HasPrefix(accum, "\"")) {
		// ExtractBridgedInput returns the whole string when not JSON — only trust
		// it when unmarshalling succeeded (object form).
		var obj map[string]any
		if err := json.Unmarshal([]byte(accum), &obj); err == nil {
			return ExtractBridgedInput(accum)
		}
	}
	// Partial: look for "input" / "patch" string prefix patterns.
	for _, key := range []string{`"input"`, `"patch"`} {
		idx := strings.Index(accum, key)
		if idx < 0 {
			continue
		}
		rest := accum[idx+len(key):]
		// skip whitespace and colon
		rest = strings.TrimLeft(rest, " \t\n\r")
		if !strings.HasPrefix(rest, ":") {
			continue
		}
		rest = strings.TrimLeft(rest[1:], " \t\n\r")
		if !strings.HasPrefix(rest, `"`) {
			continue
		}
		// Decode a possibly truncated JSON string after the opening quote.
		return decodePartialJSONString(rest[1:])
	}
	return ""
}

// decodePartialJSONString decodes a JSON string body that may be truncated
// (no closing quote yet). Returns the decoded runes so far.
func decodePartialJSONString(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			// End of string.
			break
		}
		if c == '\\' {
			if i+1 >= len(s) {
				// Trailing escape — wait for more data.
				break
			}
			i++
			switch s[i] {
			case '"', '\\', '/':
				b.WriteByte(s[i])
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'u':
				// Incomplete \uXXXX — stop.
				if i+4 >= len(s) {
					return b.String()
				}
				var r rune
				for j := 0; j < 4; j++ {
					i++
					h := s[i]
					var v rune
					switch {
					case h >= '0' && h <= '9':
						v = rune(h - '0')
					case h >= 'a' && h <= 'f':
						v = rune(h-'a') + 10
					case h >= 'A' && h <= 'F':
						v = rune(h-'A') + 10
					default:
						return b.String()
					}
					r = r<<4 | v
				}
				b.WriteRune(r)
			default:
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
