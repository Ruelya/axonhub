package biz

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

// EnsureOutputTextAnnotations walks a JSON document and ensures every object with
// type == "output_text" has a non-null "annotations" field (defaults to []).
// Existing non-null annotations are left unchanged. Returns (original, false) when
// nothing changed or JSON is invalid.
func EnsureOutputTextAnnotations(data []byte) ([]byte, bool) {
	if len(data) == 0 {
		return data, false
	}

	// Fast path: skip binary / non-JSON / obvious non-candidates.
	trim := bytes.TrimSpace(data)
	if len(trim) == 0 || (trim[0] != '{' && trim[0] != '[') {
		return data, false
	}
	if !bytes.Contains(data, []byte(`output_text`)) {
		return data, false
	}

	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return data, false
	}

	if !ensureAnnotationsInValue(root) {
		return data, false
	}

	out, err := json.Marshal(root)
	if err != nil {
		return data, false
	}
	return out, true
}

func ensureAnnotationsInValue(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		changed := false
		if typ, ok := x["type"].(string); ok && typ == "output_text" {
			ann, exists := x["annotations"]
			if !exists || ann == nil {
				x["annotations"] = []any{}
				changed = true
			}
		}
		for _, child := range x {
			if ensureAnnotationsInValue(child) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for _, child := range x {
			if ensureAnnotationsInValue(child) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

// ShouldPatchStreamEventType reports whether an SSE event type is likely to carry
// output_text payloads that need annotations patching.
func ShouldPatchStreamEventType(eventType string) bool {
	t := strings.ToLower(strings.TrimSpace(eventType))
	if t == "" {
		// Some SSE events put type only inside data JSON.
		return true
	}
	switch {
	case t == "response.completed":
		return true
	case strings.HasPrefix(t, "response.content_part."):
		return true
	case strings.HasPrefix(t, "response.output_item."):
		return true
	case t == "message" || t == "response":
		// Generic wrappers sometimes used by aggregators.
		return true
	default:
		return false
	}
}

// PatchStreamEventData applies enabled patches to a single SSE event payload.
func PatchStreamEventData(data []byte, patches ClientPatchConfig) ([]byte, bool) {
	if !patches.EnsureOutputTextAnnotations {
		return data, false
	}
	return EnsureOutputTextAnnotations(data)
}

// PatchResponseBody applies enabled patches to a non-stream response body.
func PatchResponseBody(body []byte, patches ClientPatchConfig) ([]byte, bool) {
	if !patches.EnsureOutputTextAnnotations {
		return body, false
	}
	return EnsureOutputTextAnnotations(body)
}

// clientCompatStream wraps an upstream stream and patches events for the client wire.
type clientCompatStream struct {
	inner   streams.Stream[*httpclient.StreamEvent]
	patches ClientPatchConfig
	current *httpclient.StreamEvent
}

// WrapClientCompatStream returns a stream that applies patches on Current().
// If patches are inactive, the original stream is returned unchanged.
func WrapClientCompatStream(inner streams.Stream[*httpclient.StreamEvent], patches ClientPatchConfig, active bool) streams.Stream[*httpclient.StreamEvent] {
	if inner == nil || !active || !patches.EnsureOutputTextAnnotations {
		return inner
	}
	return &clientCompatStream{inner: inner, patches: patches}
}

func (s *clientCompatStream) Next() bool {
	if !s.inner.Next() {
		s.current = nil
		return false
	}
	ev := s.inner.Current()
	if ev == nil {
		s.current = nil
		return true
	}

	// Copy so we never mutate the upstream/persistence buffer in place.
	cp := *ev
	if ShouldPatchStreamEventType(cp.Type) || (len(cp.Data) > 0 && bytes.Contains(cp.Data, []byte("output_text"))) {
		if patched, ok := PatchStreamEventData(cp.Data, s.patches); ok {
			cp.Data = patched
		}
	}
	s.current = &cp
	return true
}

func (s *clientCompatStream) Current() *httpclient.StreamEvent {
	return s.current
}

func (s *clientCompatStream) Err() error {
	return s.inner.Err()
}

func (s *clientCompatStream) Close() error {
	return s.inner.Close()
}
