package shared

import (
	"encoding/json"
	"testing"

	"github.com/looplj/axonhub/llm"
	"github.com/stretchr/testify/require"
)

// Real flat tool names observed on grok-4.5 requests #74413-#74420 (and MCP era).
func TestRestoreRealGrokNamespaceNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		flat, wantName, wantNS string
	}{
		{"agents__spawn_agent", "spawn_agent", "agents"},
		{"agents__list_agents", "list_agents", "agents"},
		{"agents__wait_agent", "wait_agent", "agents"},
		{"agents__send_message", "send_message", "agents"},
		{"agents__followup_task", "followup_task", "agents"},
		{"agents__interrupt_agent", "interrupt_agent", "agents"},
		{"mcp__fastctx__glob", "glob", "mcp__fastctx"},
		{"mcp__fastctx__grep", "grep", "mcp__fastctx"},
		{"mcp__fastctx__read", "read", "mcp__fastctx"},
		{"mcp__context7__resolve_library_id", "resolve_library_id", "mcp__context7"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.flat, func(t *testing.T) {
			t.Parallel()
			got := RestoreNamespaceToolCall(llm.ToolCall{
				Function: llm.FunctionCall{Name: tc.flat, Arguments: `{"task_name":"ok_test"}`},
			})
			require.Equal(t, tc.wantName, got.Function.Name)
			require.Equal(t, tc.wantNS, got.Function.Namespace)
			// round-trip
			flat2 := FlattenNamespaceToolCall(got)
			require.Equal(t, tc.flat, flat2.Function.Name)
			require.Equal(t, "", flat2.Function.Namespace)
		})
	}
}

func TestRestoreRealResponseLike74418(t *testing.T) {
	t.Parallel()
	// Shape of model output that previously caused unsupported call
	resp := &llm.Response{Choices: []llm.Choice{{
		Message: &llm.Message{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{
				{ID: "call-spawn", Type: "function", Function: llm.FunctionCall{
					Name: "agents__spawn_agent",
					Arguments: `{"task_name":"ok_test","message":"Reply with exactly: OK","agent_type":"trellis-implement"}`,
				}},
				{ID: "call-list", Type: "function", Function: llm.FunctionCall{
					Name: "agents__list_agents", Arguments: `{}`,
				}},
				{ID: "call-wait", Type: "function", Function: llm.FunctionCall{
					Name: "agents__wait_agent", Arguments: `{"timeout_ms":120000}`,
				}},
			},
		},
	}}}
	out := RestoreNamespaceToolCallsOnResponse(resp)
	require.NotNil(t, out.Choices[0].Message)
	calls := out.Choices[0].Message.ToolCalls
	require.Len(t, calls, 3)
	require.Equal(t, "spawn_agent", calls[0].Function.Name)
	require.Equal(t, "agents", calls[0].Function.Namespace)
	require.Equal(t, "list_agents", calls[1].Function.Name)
	require.Equal(t, "agents", calls[1].Function.Namespace)
	require.Equal(t, "wait_agent", calls[2].Function.Name)
	require.Equal(t, "agents", calls[2].Function.Namespace)
	// arguments preserved
	require.Contains(t, calls[0].Function.Arguments, "trellis-implement")
	b, _ := json.Marshal(calls)
	t.Logf("restored tool_calls: %s", string(b))
}
