package shared

import (
	"strings"

	"github.com/looplj/axonhub/llm"
)

// JoinNamespaceFunctionName encodes a Responses namespace tool as a flat function
// name for providers that only understand standard function tools.
// Must stay symmetric with SplitNamespaceFunctionName (last "__" split).
func JoinNamespaceFunctionName(namespace, function string) string {
	namespace = strings.TrimSpace(namespace)
	function = strings.TrimSpace(function)
	if namespace == "" {
		return function
	}
	if function == "" {
		return namespace
	}
	return namespace + "__" + function
}

// SplitNamespaceFunctionName reverses JoinNamespaceFunctionName.
// Uses the last "__" so namespaces that themselves contain "__"
// (e.g. mcp__fastctx + glob → mcp__fastctx__glob) round-trip correctly.
func SplitNamespaceFunctionName(flat string) (namespace, function string, ok bool) {
	flat = strings.TrimSpace(flat)
	if flat == "" {
		return "", "", false
	}
	i := strings.LastIndex(flat, "__")
	if i <= 0 || i+2 >= len(flat) {
		return "", flat, false
	}
	namespace = flat[:i]
	function = flat[i+2:]
	if namespace == "" || function == "" {
		return "", flat, false
	}
	return namespace, function, true
}

// FlattenNamespaceToolCall rewrites namespace-qualified tool calls to a flat
// function name so Chat Completions / xAI / Anthropic tool lists match history.
func FlattenNamespaceToolCall(tc llm.ToolCall) llm.ToolCall {
	if tc.ResponseCustomToolCall != nil {
		return tc
	}
	if strings.TrimSpace(tc.Function.Namespace) == "" {
		return tc
	}
	out := tc
	out.Function.Name = JoinNamespaceFunctionName(tc.Function.Namespace, tc.Function.Name)
	out.Function.Namespace = ""
	return out
}

// RestoreNamespaceToolCall rewrites flat names produced by JoinNamespaceFunctionName
// back into (name, namespace) so Codex can dispatch agents / MCP handlers.
func RestoreNamespaceToolCall(tc llm.ToolCall) llm.ToolCall {
	if tc.ResponseCustomToolCall != nil {
		return tc
	}
	// Already namespaced — leave alone.
	if strings.TrimSpace(tc.Function.Namespace) != "" {
		return tc
	}
	ns, fn, ok := SplitNamespaceFunctionName(tc.Function.Name)
	if !ok {
		return tc
	}
	out := tc
	out.Function.Name = fn
	out.Function.Namespace = ns
	return out
}

// FlattenNamespaceToolCallsInRequest flattens tool-call history for providers
// that cannot express Responses namespace tools. Tool *definitions* are already
// flattened in responses inbound convertTools; this covers message history so
// names stay consistent with the flattened tool list.
func FlattenNamespaceToolCallsInRequest(req *llm.Request) *llm.Request {
	if req == nil {
		return nil
	}
	if !requestNeedsNamespaceFlatten(req) {
		return req
	}

	cloned := *req
	cloned.Messages = flattenNamespaceMessages(req.Messages)
	return &cloned
}

func requestNeedsNamespaceFlatten(req *llm.Request) bool {
	for _, msg := range req.Messages {
		for _, tc := range msg.ToolCalls {
			if tc.ResponseCustomToolCall == nil && strings.TrimSpace(tc.Function.Namespace) != "" {
				return true
			}
		}
	}
	return false
}

func flattenNamespaceMessages(messages []llm.Message) []llm.Message {
	if len(messages) == 0 {
		return messages
	}
	out := make([]llm.Message, len(messages))
	for i, msg := range messages {
		out[i] = msg
		if len(msg.ToolCalls) == 0 {
			continue
		}
		calls := make([]llm.ToolCall, len(msg.ToolCalls))
		for j, tc := range msg.ToolCalls {
			calls[j] = FlattenNamespaceToolCall(tc)
		}
		out[i].ToolCalls = calls
	}
	return out
}

// RestoreNamespaceToolCallsOnResponse rewrites flat namespace tool names on the
// way back to a Responses client (Codex).
func RestoreNamespaceToolCallsOnResponse(resp *llm.Response) *llm.Response {
	if resp == nil || !responseNeedsNamespaceRestore(resp) {
		return resp
	}

	cloned := *resp
	cloned.Choices = make([]llm.Choice, len(resp.Choices))
	for i, choice := range resp.Choices {
		cloned.Choices[i] = choice
		if choice.Message != nil {
			msg := *choice.Message
			msg.ToolCalls = restoreNamespaceToolCalls(choice.Message.ToolCalls)
			cloned.Choices[i].Message = &msg
		}
		if choice.Delta != nil {
			delta := *choice.Delta
			delta.ToolCalls = restoreNamespaceToolCalls(choice.Delta.ToolCalls)
			cloned.Choices[i].Delta = &delta
		}
	}
	return &cloned
}

func responseNeedsNamespaceRestore(resp *llm.Response) bool {
	for _, choice := range resp.Choices {
		if choice.Message != nil {
			for _, tc := range choice.Message.ToolCalls {
				if namespaceToolCallNeedsRestore(tc) {
					return true
				}
			}
		}
		if choice.Delta != nil {
			for _, tc := range choice.Delta.ToolCalls {
				if namespaceToolCallNeedsRestore(tc) {
					return true
				}
			}
		}
	}
	return false
}

func namespaceToolCallNeedsRestore(tc llm.ToolCall) bool {
	if tc.ResponseCustomToolCall != nil {
		return false
	}
	if strings.TrimSpace(tc.Function.Namespace) != "" {
		return false
	}
	_, _, ok := SplitNamespaceFunctionName(tc.Function.Name)
	return ok
}

func restoreNamespaceToolCalls(calls []llm.ToolCall) []llm.ToolCall {
	if len(calls) == 0 {
		return calls
	}
	out := make([]llm.ToolCall, len(calls))
	for i, tc := range calls {
		out[i] = RestoreNamespaceToolCall(tc)
	}
	return out
}

// restoreNamespaceToolStream restores flat namespace names on every stream chunk.
type restoreNamespaceToolStream struct {
	inner responseStream
}

// NewRestoreNamespaceToolStream wraps a response stream and restores
// agents__/mcp__-style flat names on each chunk.
func NewRestoreNamespaceToolStream(inner responseStream) *restoreNamespaceToolStream {
	return &restoreNamespaceToolStream{inner: inner}
}

func (s *restoreNamespaceToolStream) Next() bool { return s.inner.Next() }

func (s *restoreNamespaceToolStream) Current() *llm.Response {
	return RestoreNamespaceToolCallsOnResponse(s.inner.Current())
}

func (s *restoreNamespaceToolStream) Err() error   { return s.inner.Err() }
func (s *restoreNamespaceToolStream) Close() error { return s.inner.Close() }
