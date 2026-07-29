package shared

import (
	"testing"

	"github.com/looplj/axonhub/llm"
	"github.com/stretchr/testify/require"
)

func TestJoinSplitNamespaceFunctionName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		ns, fn string
	}{
		{"agents", "spawn_agent"},
		{"agents", "list_agents"},
		{"mcp__fastctx", "glob"},
		{"mcp__fastctx", "read"},
		{"mcp__context7", "resolve_library_id"},
	}
	for _, tc := range cases {
		flat := JoinNamespaceFunctionName(tc.ns, tc.fn)
		ns, fn, ok := SplitNamespaceFunctionName(flat)
		require.True(t, ok, flat)
		require.Equal(t, tc.ns, ns)
		require.Equal(t, tc.fn, fn)
	}

	_, _, ok := SplitNamespaceFunctionName("spawn_agent")
	require.False(t, ok)
	_, _, ok = SplitNamespaceFunctionName("__spawn")
	require.False(t, ok)
}

func TestFlattenAndRestoreNamespaceToolCall(t *testing.T) {
	t.Parallel()

	tc := llm.ToolCall{
		ID:   "call_1",
		Type: "function",
		Function: llm.FunctionCall{
			Name:      "spawn_agent",
			Namespace: "agents",
			Arguments: `{"task_name":"x"}`,
		},
	}
	flat := FlattenNamespaceToolCall(tc)
	require.Equal(t, "agents__spawn_agent", flat.Function.Name)
	require.Equal(t, "", flat.Function.Namespace)

	restored := RestoreNamespaceToolCall(flat)
	require.Equal(t, "spawn_agent", restored.Function.Name)
	require.Equal(t, "agents", restored.Function.Namespace)
	require.Equal(t, `{"task_name":"x"}`, restored.Function.Arguments)
}

func TestFlattenNamespaceToolCallsInRequest(t *testing.T) {
	t.Parallel()

	req := &llm.Request{
		Messages: []llm.Message{
			{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{
					{
						ID:   "c1",
						Type: "function",
						Function: llm.FunctionCall{
							Name:      "spawn_agent",
							Namespace: "agents",
							Arguments: `{}`,
						},
					},
					{
						ID:   "c2",
						Type: "function",
						Function: llm.FunctionCall{
							Name:      "glob",
							Namespace: "mcp__fastctx",
							Arguments: `{"pattern":"*"}`,
						},
					},
				},
			},
		},
	}
	out := FlattenNamespaceToolCallsInRequest(req)
	require.NotSame(t, req, out)
	require.Equal(t, "agents__spawn_agent", out.Messages[0].ToolCalls[0].Function.Name)
	require.Equal(t, "", out.Messages[0].ToolCalls[0].Function.Namespace)
	require.Equal(t, "mcp__fastctx__glob", out.Messages[0].ToolCalls[1].Function.Name)
}

func TestRestoreNamespaceToolCallsOnResponse(t *testing.T) {
	t.Parallel()

	resp := &llm.Response{
		Choices: []llm.Choice{
			{
				Message: &llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{
						{
							ID:   "c1",
							Type: "function",
							Function: llm.FunctionCall{
								Name:      "agents__wait_agent",
								Arguments: `{"timeout_ms":1000}`,
							},
						},
					},
				},
			},
		},
	}
	out := RestoreNamespaceToolCallsOnResponse(resp)
	require.NotSame(t, resp, out)
	tc := out.Choices[0].Message.ToolCalls[0]
	require.Equal(t, "wait_agent", tc.Function.Name)
	require.Equal(t, "agents", tc.Function.Namespace)
}
