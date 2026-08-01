package biz

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCompatStream struct {
	events []*httpclient.StreamEvent
	i      int
	cur    *httpclient.StreamEvent
}

func (s *fakeCompatStream) Next() bool {
	if s.i >= len(s.events) {
		s.cur = nil
		return false
	}
	s.cur = s.events[s.i]
	s.i++
	return true
}

func (s *fakeCompatStream) Current() *httpclient.StreamEvent { return s.cur }
func (s *fakeCompatStream) Err() error                       { return nil }
func (s *fakeCompatStream) Close() error                     { return nil }

// Ensures WrapClientCompatStream mutates the client-facing event Data (outbound wire)
// without touching the original upstream buffer used for persistence.
func TestWrapClientCompatStream_PatchesCompletedOutbound(t *testing.T) {
	// Mirrors stored response.completed payloads that lack annotations/id/status (e.g. live Grok failures).
	data := []byte(`{"type":"response.completed","response":{"id":"resp_x","object":"response","output":[{"type":"message","role":"assistant","content":[{"text":"hello","type":"output_text"}]}],"status":"completed"}}`)
	require.NotContains(t, string(data), `"annotations"`)
	require.NotContains(t, string(data), `"id":"item_`)

	inner := &fakeCompatStream{events: []*httpclient.StreamEvent{
		{Type: "response.completed", Data: data},
	}}
	patches := ClientPatchConfig{EnsureOutputTextAnnotations: true, EnsureStrictResponsesOutput: true}
	wrapped := WrapClientCompatStream(inner, patches, true)

	require.True(t, wrapped.Next())
	cur := wrapped.Current()
	require.NotNil(t, cur)
	require.Contains(t, string(cur.Data), `"annotations":[]`)
	require.Contains(t, string(cur.Data), `"status":"completed"`)
	// original buffer not mutated
	require.NotContains(t, string(data), `"annotations"`)

	var root map[string]any
	require.NoError(t, json.Unmarshal(cur.Data, &root))
	resp := root["response"].(map[string]any)
	out := resp["output"].([]any)
	msg := out[0].(map[string]any)
	require.NotEmpty(t, msg["id"])
	assert.Equal(t, "completed", msg["status"])
	content := msg["content"].([]any)
	part := content[0].(map[string]any)
	anns, ok := part["annotations"].([]any)
	require.True(t, ok)
	assert.Len(t, anns, 0)
}

func TestWrapClientCompatStream_BackfillsIDFromOutputItemDone(t *testing.T) {
	done := []byte(`{"type":"response.output_item.done","item":{"id":"item_abc123","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hi","annotations":[]}]}}`)
	completed := []byte(`{"type":"response.completed","response":{"id":"resp_x","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}]}}`)

	inner := &fakeCompatStream{events: []*httpclient.StreamEvent{
		{Type: "response.output_item.done", Data: done},
		{Type: "response.completed", Data: completed},
	}}
	patches := ClientPatchConfig{EnsureStrictResponsesOutput: true}
	wrapped := WrapClientCompatStream(inner, patches, true)

	require.True(t, wrapped.Next()) // done
	require.True(t, wrapped.Next()) // completed
	cur := wrapped.Current()
	require.Contains(t, string(cur.Data), `"id":"item_abc123"`)
	require.Contains(t, string(cur.Data), `"annotations":[]`)
	require.True(t, bytes.Contains(cur.Data, []byte(`"status":"completed"`)))
}

func TestWrapClientCompatStream_InactiveNoop(t *testing.T) {
	data := []byte(`{"type":"output_text","text":"x"}`)
	inner := &fakeCompatStream{events: []*httpclient.StreamEvent{
		{Type: "response.completed", Data: data},
	}}
	wrapped := WrapClientCompatStream(inner, ClientPatchConfig{EnsureOutputTextAnnotations: true}, false)
	require.Same(t, inner, wrapped)
}

func TestWrapClientCompatStream_OnChangedOnlyWhenModified(t *testing.T) {
	// Already-normalized payload: no field changes expected.
	clean := []byte(`{"type":"response.completed","response":{"id":"resp_x","object":"response","output":[{"type":"message","id":"item_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hi","annotations":[]}]}],"status":"completed"}}`)
	// Thin payload that needs annotations.
	thin := []byte(`{"type":"response.completed","response":{"id":"resp_x","object":"response","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"status":"completed"}}`)

	patches := ClientPatchConfig{EnsureOutputTextAnnotations: true, EnsureStrictResponsesOutput: true}

	nClean := 0
	w1 := WrapClientCompatStreamWithNotify(&fakeCompatStream{events: []*httpclient.StreamEvent{
		{Type: "response.completed", Data: clean},
	}}, patches, true, func() { nClean++ })
	require.True(t, w1.Next())
	assert.Equal(t, 0, nClean)
	assert.False(t, ClientCompatStreamChanged(w1))

	nThin := 0
	w2 := WrapClientCompatStreamWithNotify(&fakeCompatStream{events: []*httpclient.StreamEvent{
		{Type: "response.completed", Data: thin},
		{Type: "response.completed", Data: append([]byte(nil), thin...)},
	}}, patches, true, func() { nThin++ })
	require.True(t, w2.Next())
	require.True(t, w2.Next())
	assert.Equal(t, 1, nThin, "onChanged should fire only once")
	assert.True(t, ClientCompatStreamChanged(w2))
}

func TestNormalizeResponsesJSON_LiveSkinnyCompleted(t *testing.T) {
	// Exact shape from root-cause writeup / live SSE.
	input := []byte(`{
		"type":"response.completed",
		"response":{
			"output":[{
				"type":"message",
				"role":"assistant",
				"content":[{"type":"output_text","text":"hi"}]
			}]
		}
	}`)
	out, changed := NormalizeResponsesJSON(input, nil)
	require.True(t, changed)

	var root map[string]any
	require.NoError(t, json.Unmarshal(out, &root))
	resp := root["response"].(map[string]any)
	msg := resp["output"].([]any)[0].(map[string]any)
	require.NotEmpty(t, msg["id"])
	assert.Equal(t, "completed", msg["status"])
	assert.Equal(t, "assistant", msg["role"])
	part := msg["content"].([]any)[0].(map[string]any)
	_, ok := part["annotations"].([]any)
	require.True(t, ok, "annotations must be present as array")
}

func TestNormalizeResponsesJSON_StripEmptyConversation(t *testing.T) {
	input := []byte(`{"response":{"conversation":{},"output":[]}}`)
	out, changed := NormalizeResponsesJSON(input, nil)
	require.True(t, changed)
	assert.NotContains(t, string(out), `"conversation"`)

	keep := []byte(`{"response":{"conversation":{"id":"c_1"},"output":[]}}`)
	out2, changed2 := NormalizeResponsesJSON(keep, nil)
	// may still change nothing if no other fixes
	if changed2 {
		assert.Contains(t, string(out2), `"c_1"`)
	} else {
		assert.Contains(t, string(keep), `"c_1"`)
	}
	// ensure we don't strip valid conversation
	out3, _ := NormalizeResponsesJSON(keep, nil)
	assert.Contains(t, string(out3), `"conversation"`)
	assert.Contains(t, string(out3), `"c_1"`)
}

func TestNormalizeResponsesJSON_PreservesExistingAnnotations(t *testing.T) {
	input := []byte(`{"type":"output_text","text":"x","annotations":[{"type":"url_citation"}]}`)
	_, changed := NormalizeResponsesJSON(input, nil)
	assert.False(t, changed)
}

func TestNormalizeResponsesJSON_BackfillOpts(t *testing.T) {
	input := []byte(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"a"}]}]}`)
	out, changed := NormalizeResponsesJSON(input, &NormalizeOptions{MessageIDs: []string{"item_from_stream"}})
	require.True(t, changed)
	assert.Contains(t, string(out), `"id":"item_from_stream"`)
}
