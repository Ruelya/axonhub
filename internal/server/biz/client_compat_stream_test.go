package biz

import (
	"bytes"
	"testing"

	"github.com/looplj/axonhub/llm/httpclient"
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
	// Mirrors stored response.completed payloads that lack annotations (e.g. req 77326).
	data := []byte(`{"type":"response.completed","response":{"id":"resp_x","object":"response","output":[{"id":"item_1","type":"message","role":"assistant","status":"completed","content":[{"text":"hello","type":"output_text"}]}],"status":"completed"}}`)
	require.NotContains(t, string(data), `"annotations"`)

	inner := &fakeCompatStream{events: []*httpclient.StreamEvent{
		{Type: "response.completed", Data: data},
	}}
	patches := ClientPatchConfig{EnsureOutputTextAnnotations: true}
	wrapped := WrapClientCompatStream(inner, patches, true)

	require.True(t, wrapped.Next())
	cur := wrapped.Current()
	require.NotNil(t, cur)
	require.Contains(t, string(cur.Data), `"annotations":[]`)
	// Persistence buffer must stay raw (pre-patch).
	require.NotContains(t, string(data), `"annotations"`)
	require.True(t, bytes.Contains(cur.Data, []byte(`"annotations":[]`)))
}

func TestWrapClientCompatStream_InactiveNoop(t *testing.T) {
	data := []byte(`{"type":"output_text","text":"x"}`)
	inner := &fakeCompatStream{events: []*httpclient.StreamEvent{
		{Type: "response.completed", Data: data},
	}}
	wrapped := WrapClientCompatStream(inner, ClientPatchConfig{EnsureOutputTextAnnotations: true}, false)
	require.Same(t, inner, wrapped)
}
