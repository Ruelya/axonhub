package shared

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
)

func TestShouldBridgeCustomTools(t *testing.T) {
	t.Parallel()

	// Codex Responses inbound + freeform-broken channels → bridge.
	require.True(t, ShouldBridgeCustomTools(ChannelTypeXaiResponses, llm.APIFormatOpenAIResponse))
	require.True(t, ShouldBridgeCustomTools("xai_responses", llm.APIFormatOpenAIResponseCompact))
	require.True(t, ShouldBridgeCustomTools("anthropic", llm.APIFormatOpenAIResponse))
	require.True(t, ShouldBridgeCustomTools("xai", llm.APIFormatOpenAIResponse))
	require.True(t, ShouldBridgeCustomTools("openai", llm.APIFormatOpenAIResponse))
	require.True(t, ShouldBridgeCustomTools("claudecode", llm.APIFormatOpenAIResponse))
	require.True(t, ShouldBridgeCustomTools("gemini", llm.APIFormatOpenAIResponse))

	// Native Responses-family: no bridge (preserves pass-through for codex channels).
	require.False(t, ShouldBridgeCustomTools("openai_responses", llm.APIFormatOpenAIResponse))
	require.False(t, ShouldBridgeCustomTools("codex", llm.APIFormatOpenAIResponse))
	require.False(t, ShouldBridgeCustomTools("codex", llm.APIFormatOpenAIResponseCompact))

	// Chat Completions inbound is never bridged here.
	require.False(t, ShouldBridgeCustomTools(ChannelTypeXaiResponses, llm.APIFormatOpenAIChatCompletion))
	require.False(t, ShouldBridgeCustomTools("anthropic", llm.APIFormatOpenAIChatCompletion))
	require.False(t, ShouldBridgeCustomTools("", llm.APIFormatOpenAIResponse))
	// Unknown channel types: do not bridge by default.
	require.False(t, ShouldBridgeCustomTools("some_future_type", llm.APIFormatOpenAIResponse))
}

func TestBridgeRequestForOutbound_ToolsAndHistory(t *testing.T) {
	t.Parallel()

	patch := "*** Begin Patch\n*** Update File: a.go\n@@\n-old\n+new\n*** End Patch"
	req := &llm.Request{
		Model:     "grok-4.5",
		APIFormat: llm.APIFormatOpenAIResponse,
		Tools: []llm.Tool{
			{
				Type: llm.ToolTypeResponsesCustomTool,
				ResponseCustomTool: &llm.ResponseCustomTool{
					Name:        "apply_patch",
					Description: "Edit files with a patch.",
				},
			},
			{
				Type: llm.ToolTypeFunction,
				Function: llm.Function{
					Name:        "shell_command",
					Description: "Run shell",
					Parameters:  json.RawMessage(`{"type":"object"}`),
				},
			},
		},
		Messages: []llm.Message{
			{Role: "user", Content: llm.MessageContent{Content: strPtr("edit a.go")}},
			{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{
					{
						ID:   "call_1",
						Type: llm.ToolTypeResponsesCustomTool,
						ResponseCustomToolCall: &llm.ResponseCustomToolCall{
							CallID: "call_1",
							Name:   "apply_patch",
							Input:  patch,
						},
					},
				},
			},
			{
				Role:       "tool",
				ToolCallID: strPtr("call_1"),
				Content:    llm.MessageContent{Content: strPtr("Success")},
			},
		},
	}

	decision := NewBridgeDecision(ChannelTypeXaiResponses, llm.APIFormatOpenAIResponse)
	out := BridgeRequestForOutbound(req, decision)
	require.NotSame(t, req, out)

	// apply_patch became function; shell stayed function.
	require.Len(t, out.Tools, 2)
	require.Equal(t, llm.ToolTypeFunction, out.Tools[0].Type)
	require.Equal(t, "apply_patch", out.Tools[0].Function.Name)
	require.Contains(t, string(out.Tools[0].Function.Parameters), `"input"`)
	require.Equal(t, "shell_command", out.Tools[1].Function.Name)

	// History custom_tool_call → function_call with JSON args.
	require.Len(t, out.Messages[1].ToolCalls, 1)
	tc := out.Messages[1].ToolCalls[0]
	require.Equal(t, llm.ToolTypeFunction, tc.Type)
	require.Equal(t, "apply_patch", tc.Function.Name)
	require.Nil(t, tc.ResponseCustomToolCall)
	require.Equal(t, patch, ExtractBridgedInput(tc.Function.Arguments))
	require.Equal(t, "call_1", tc.ID)

	// Tool result id preserved.
	require.Equal(t, "call_1", *out.Messages[2].ToolCallID)

	// Metadata set.
	require.True(t, out.TransformerMetadata[MetaCustomToolBridgeEnabled].(bool))

	// Original request untouched.
	require.Equal(t, llm.ToolTypeResponsesCustomTool, req.Tools[0].Type)
}

func TestBridgeRequestForOutbound_SkipsNativeOpenAIResponsesChannel(t *testing.T) {
	t.Parallel()

	req := &llm.Request{
		Model: "gpt-5.6-sol",
		Tools: []llm.Tool{{
			Type: llm.ToolTypeResponsesCustomTool,
			ResponseCustomTool: &llm.ResponseCustomTool{
				Name: "apply_patch",
			},
		}},
	}
	decision := NewBridgeDecision("openai_responses", llm.APIFormatOpenAIResponse)
	out := BridgeRequestForOutbound(req, decision)
	require.Same(t, req, out)
	require.Equal(t, llm.ToolTypeResponsesCustomTool, out.Tools[0].Type)
}

func TestBridgeRequestForOutbound_AlwaysClonesWhenDecisionEnabled(t *testing.T) {
	t.Parallel()

	// No apply_patch in tools — still clone so metadata is set for response restore.
	req := &llm.Request{
		Model:     "grok-4.5",
		APIFormat: llm.APIFormatOpenAIResponse,
		Tools: []llm.Tool{{
			Type: llm.ToolTypeFunction,
			Function: llm.Function{
				Name: "shell_command",
			},
		}},
	}
	decision := NewBridgeDecision(ChannelTypeXaiResponses, llm.APIFormatOpenAIResponse)
	out := BridgeRequestForOutbound(req, decision)
	require.NotSame(t, req, out)
	require.True(t, out.TransformerMetadata[MetaCustomToolBridgeEnabled].(bool))
	require.Equal(t, "shell_command", out.Tools[0].Function.Name)
}

func TestRestoreCustomToolCallsOnResponse(t *testing.T) {
	t.Parallel()

	patch := "*** Begin Patch\n*** End Patch"
	args, err := json.Marshal(map[string]string{"input": patch})
	require.NoError(t, err)

	resp := &llm.Response{
		Choices: []llm.Choice{{
			Message: &llm.Message{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{{
					ID:   "call_xyz",
					Type: llm.ToolTypeFunction,
					Function: llm.FunctionCall{
						Name:      "apply_patch",
						Arguments: string(args),
					},
				}},
			},
		}},
	}

	decision := NewBridgeDecision(ChannelTypeXaiResponses, llm.APIFormatOpenAIResponse)
	out := RestoreCustomToolCallsOnResponse(resp, decision)
	require.NotNil(t, out.Choices[0].Message)
	tc := out.Choices[0].Message.ToolCalls[0]
	require.Equal(t, llm.ToolTypeResponsesCustomTool, tc.Type)
	require.NotNil(t, tc.ResponseCustomToolCall)
	require.Equal(t, "apply_patch", tc.ResponseCustomToolCall.Name)
	require.Equal(t, patch, tc.ResponseCustomToolCall.Input)
	require.Equal(t, "call_xyz", tc.ResponseCustomToolCall.CallID)
}

func TestExtractBridgedInput(t *testing.T) {
	t.Parallel()

	require.Equal(t, "hello", ExtractBridgedInput(`{"input":"hello"}`))
	require.Equal(t, "p", ExtractBridgedInput(`{"patch":"p"}`))
	require.Equal(t, "*** Begin Patch", ExtractBridgedInput("*** Begin Patch"))
	require.Equal(t, "", ExtractBridgedInput(""))
}

func TestRestoreCustomToolStream_ProgressiveInput(t *testing.T) {
	t.Parallel()

	decision := NewBridgeDecision(ChannelTypeXaiResponses, llm.APIFormatOpenAIResponse)
	chunks := []*llm.Response{
		{
			Choices: []llm.Choice{{
				Delta: &llm.Message{
					ToolCalls: []llm.ToolCall{{
						ID:   "c1",
						Type: llm.ToolTypeFunction,
						Function: llm.FunctionCall{
							Name:      "apply_patch",
							Arguments: `{"input":"`,
						},
					}},
				},
			}},
		},
		{
			Choices: []llm.Choice{{
				Delta: &llm.Message{
					ToolCalls: []llm.ToolCall{{
						ID:   "c1",
						Type: llm.ToolTypeFunction,
						Function: llm.FunctionCall{
							Name:      "apply_patch",
							Arguments: `*** Begin Patch\n*** End Patch"}`,
						},
					}},
				},
			}},
		},
	}

	stream := NewRestoreCustomToolStream(&sliceResponseStream{items: chunks}, decision)
	require.True(t, stream.Next())
	// First chunk may not yet have a closed JSON string — input delta can be empty.
	first := stream.Current()
	require.NotNil(t, first.Choices[0].Delta)
	require.Equal(t, llm.ToolTypeResponsesCustomTool, first.Choices[0].Delta.ToolCalls[0].Type)

	require.True(t, stream.Next())
	second := stream.Current()
	tc := second.Choices[0].Delta.ToolCalls[0]
	require.Equal(t, llm.ToolTypeResponsesCustomTool, tc.Type)
	require.Contains(t, tc.ResponseCustomToolCall.Input, "Begin Patch")
	require.False(t, stream.Next())
}

// Regression: xAI/OpenAI streams often send name only on the first delta, then
// bare argument fragments with empty name/id. Old keying used idx+name so those
// fragments never joined and Codex saw custom_tool_call input="".
func TestRestoreCustomToolStream_NameOnlyThenArgsOnly(t *testing.T) {
	t.Parallel()

	decision := NewBridgeDecision(ChannelTypeXaiResponses, llm.APIFormatOpenAIResponse)
	patch := "*** Begin Patch\n*** Update File: a.txt\n@@\n-old\n+new\n*** End Patch\n"
	args, err := json.Marshal(map[string]string{"input": patch})
	require.NoError(t, err)

	chunks := []*llm.Response{
		{
			Choices: []llm.Choice{{
				Delta: &llm.Message{
					ToolCalls: []llm.ToolCall{{
						ID:    "fc_1",
						Index: 0,
						Type:  llm.ToolTypeFunction,
						Function: llm.FunctionCall{
							Name:      "apply_patch",
							Arguments: "",
						},
					}},
				},
			}},
		},
		{
			Choices: []llm.Choice{{
				Delta: &llm.Message{
					ToolCalls: []llm.ToolCall{{
						// No id, no name — only index + arguments (real provider shape).
						Index: 0,
						Type:  llm.ToolTypeFunction,
						Function: llm.FunctionCall{
							Arguments: string(args),
						},
					}},
				},
			}},
		},
	}

	stream := NewRestoreCustomToolStream(&sliceResponseStream{items: chunks}, decision)
	require.True(t, stream.Next())
	first := stream.Current().Choices[0].Delta.ToolCalls[0]
	require.Equal(t, llm.ToolTypeResponsesCustomTool, first.Type)
	require.Equal(t, "apply_patch", first.ResponseCustomToolCall.Name)
	require.Equal(t, "fc_1", first.ResponseCustomToolCall.CallID)

	require.True(t, stream.Next())
	second := stream.Current().Choices[0].Delta.ToolCalls[0]
	require.Equal(t, llm.ToolTypeResponsesCustomTool, second.Type)
	require.Equal(t, "apply_patch", second.ResponseCustomToolCall.Name)
	require.Equal(t, "fc_1", second.ResponseCustomToolCall.CallID)
	require.Equal(t, patch, second.ResponseCustomToolCall.Input)
	require.False(t, stream.Next())
}

func TestBridgeDecisionFromRequest(t *testing.T) {
	t.Parallel()

	req := &llm.Request{
		TransformerMetadata: map[string]any{
			MetaCustomToolBridgeEnabled: true,
			MetaCustomToolBridgeNames:   []string{"apply_patch"},
		},
	}
	d := BridgeDecisionFromRequest(req)
	require.True(t, d.Enabled)
	_, ok := d.Names["apply_patch"]
	require.True(t, ok)
}

func strPtr(s string) *string { return &s }

// sliceResponseStream is a tiny test double for streams.Stream[*llm.Response].
type sliceResponseStream struct {
	items []*llm.Response
	i     int
	cur   *llm.Response
}

func (s *sliceResponseStream) Next() bool {
	if s.i >= len(s.items) {
		s.cur = nil
		return false
	}
	s.cur = s.items[s.i]
	s.i++
	return true
}

func (s *sliceResponseStream) Current() *llm.Response { return s.cur }
func (s *sliceResponseStream) Err() error             { return nil }
func (s *sliceResponseStream) Close() error           { return nil }
