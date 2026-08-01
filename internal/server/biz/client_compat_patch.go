package biz

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

// ClientPatchConfig.AnyActive reports whether any wire patch should run.
func (p ClientPatchConfig) AnyActive() bool {
	return p.EnsureOutputTextAnnotations || p.EnsureStrictResponsesOutput
}

// NormalizeOptions carries optional stream context for id backfill.
type NormalizeOptions struct {
	// MessageIDs is a FIFO of ids observed on output_item.done (type=message).
	// Consumed when a message item is missing id.
	MessageIDs []string
}

// EnsureOutputTextAnnotations walks a JSON document and ensures every object with
// type == "output_text" has a non-null "annotations" field (defaults to []).
// Existing non-null annotations are left unchanged. Returns (original, false) when
// nothing changed or JSON is invalid.
//
// Prefer NormalizeResponsesJSON for Grok Build strict clients (also fills message id/status).
func EnsureOutputTextAnnotations(data []byte) ([]byte, bool) {
	return NormalizeResponsesJSON(data, nil)
}

// NormalizeResponsesJSON applies Grok-strict Responses field fixes on a JSON document:
//   - output_text: missing/null annotations → []
//   - message items: ensure id, status (default "completed"), role (default "assistant")
//   - conversation: strip empty objects / objects without id
//
// opts may supply MessageIDs for stream backfill (consumed in order).
func NormalizeResponsesJSON(data []byte, opts *NormalizeOptions) ([]byte, bool) {
	if len(data) == 0 {
		return data, false
	}

	trim := bytes.TrimSpace(data)
	if len(trim) == 0 || (trim[0] != '{' && trim[0] != '[') {
		return data, false
	}

	// Fast path: skip documents that cannot need these fixes.
	if !bytes.Contains(data, []byte(`output_text`)) &&
		!bytes.Contains(data, []byte(`"type":"message"`)) &&
		!bytes.Contains(data, []byte(`"type": "message"`)) &&
		!bytes.Contains(data, []byte(`conversation`)) {
		return data, false
	}

	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return data, false
	}

	st := &normState{}
	if opts != nil && len(opts.MessageIDs) > 0 {
		st.messageIDs = append([]string(nil), opts.MessageIDs...)
	}

	if !normalizeValue(root, st) {
		return data, false
	}

	out, err := json.Marshal(root)
	if err != nil {
		return data, false
	}
	return out, true
}

type normState struct {
	// messageIDs aligned by message-item order in the stream (output_item.done).
	messageIDs []string
	msgIndex   int // count of message items seen (index into messageIDs / synthetic seed)
}

func (s *normState) idForMessageIndex(idx int) (string, bool) {
	if s == nil || idx < 0 || idx >= len(s.messageIDs) {
		return "", false
	}
	id := strings.TrimSpace(s.messageIDs[idx])
	return id, id != ""
}

func normalizeValue(v any, st *normState) bool {
	switch x := v.(type) {
	case map[string]any:
		changed := false

		// conversation: {} or missing id → drop key
		if conv, ok := x["conversation"]; ok {
			if shouldStripConversation(conv) {
				delete(x, "conversation")
				changed = true
			}
		}

		typ, _ := x["type"].(string)
		switch typ {
		case "output_text":
			if ensureOutputTextAnnotationsOnMap(x) {
				changed = true
			}
		case "message":
			if ensureMessageFields(x, st) {
				changed = true
			}
		}

		for _, child := range x {
			if normalizeValue(child, st) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for _, child := range x {
			if normalizeValue(child, st) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

func ensureOutputTextAnnotationsOnMap(x map[string]any) bool {
	ann, exists := x["annotations"]
	if !exists || ann == nil {
		x["annotations"] = []any{}
		return true
	}
	return false
}

func ensureMessageFields(x map[string]any, st *normState) bool {
	changed := false
	idx := 0
	if st != nil {
		idx = st.msgIndex
		st.msgIndex++
	}

	// id — prefer stream-aligned hint at the same message index; else synthesize.
	if !hasNonEmptyString(x, "id") {
		if id, ok := st.idForMessageIndex(idx); ok {
			x["id"] = id
			changed = true
		} else {
			x["id"] = syntheticMessageID(idx, x)
			changed = true
		}
	}

	// status
	if !hasNonEmptyString(x, "status") {
		x["status"] = "completed"
		changed = true
	}

	// role (defensive)
	if !hasNonEmptyString(x, "role") {
		x["role"] = "assistant"
		changed = true
	}

	return changed
}

func hasNonEmptyString(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok || v == nil {
		return false
	}
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) != ""
}

func shouldStripConversation(conv any) bool {
	m, ok := conv.(map[string]any)
	if !ok {
		// null / non-object: strip
		return true
	}
	return !hasNonEmptyString(m, "id")
}

func syntheticMessageID(index int, msg map[string]any) string {
	role, _ := msg["role"].(string)
	text := firstOutputText(msg)
	sum := sha1.Sum([]byte(fmt.Sprintf("%d|%s|%s", index, role, text)))
	return "msg_" + hex.EncodeToString(sum[:8])
}

func firstOutputText(msg map[string]any) string {
	content, ok := msg["content"]
	if !ok {
		return ""
	}
	arr, ok := content.([]any)
	if !ok {
		return ""
	}
	for _, c := range arr {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := m["type"].(string); typ == "output_text" {
			if t, ok := m["text"].(string); ok {
				if len(t) > 64 {
					return t[:64]
				}
				return t
			}
		}
	}
	return ""
}

// ShouldPatchStreamEventType reports whether an SSE event type is likely to carry
// payloads that need Responses strict field patching.
func ShouldPatchStreamEventType(eventType string) bool {
	t := strings.ToLower(strings.TrimSpace(eventType))
	if t == "" {
		// Some SSE events put type only inside data JSON.
		return true
	}
	switch {
	case t == "response.completed", t == "response.incomplete", t == "response.failed":
		return true
	case strings.HasPrefix(t, "response.content_part."):
		return true
	case strings.HasPrefix(t, "response.output_item."):
		return true
	case t == "message" || t == "response":
		return true
	default:
		return false
	}
}

// PatchStreamEventData applies enabled patches to a single SSE event payload.
func PatchStreamEventData(data []byte, patches ClientPatchConfig) ([]byte, bool) {
	return PatchStreamEventDataWithOpts(data, patches, nil)
}

// PatchStreamEventDataWithOpts applies patches with optional stream id backfill context.
func PatchStreamEventDataWithOpts(data []byte, patches ClientPatchConfig, opts *NormalizeOptions) ([]byte, bool) {
	if !patches.AnyActive() {
		return data, false
	}
	return NormalizeResponsesJSON(data, opts)
}

// PatchResponseBody applies enabled patches to a non-stream response body.
func PatchResponseBody(body []byte, patches ClientPatchConfig) ([]byte, bool) {
	if !patches.AnyActive() {
		return body, false
	}
	return NormalizeResponsesJSON(body, nil)
}

// streamItemHint records a message id seen on output_item.done for later completed backfill.
type streamItemHint struct {
	ID   string
	Type string
	Role string
}

// clientCompatStream wraps an upstream stream and patches events for the client wire.
type clientCompatStream struct {
	inner        streams.Stream[*httpclient.StreamEvent]
	patches      ClientPatchConfig
	current      *httpclient.StreamEvent
	messageHints []streamItemHint
	changed      bool
	onChanged    func()
}

// WrapClientCompatStream returns a stream that applies patches on Current().
// If patches are inactive, the original stream is returned unchanged.
func WrapClientCompatStream(inner streams.Stream[*httpclient.StreamEvent], patches ClientPatchConfig, active bool) streams.Stream[*httpclient.StreamEvent] {
	return WrapClientCompatStreamWithNotify(inner, patches, active, nil)
}

// WrapClientCompatStreamWithNotify is like WrapClientCompatStream but invokes onChanged
// once when any event body is actually modified (for request-log applied flag).
func WrapClientCompatStreamWithNotify(
	inner streams.Stream[*httpclient.StreamEvent],
	patches ClientPatchConfig,
	active bool,
	onChanged func(),
) streams.Stream[*httpclient.StreamEvent] {
	if inner == nil || !active || !patches.AnyActive() {
		return inner
	}
	return &clientCompatStream{inner: inner, patches: patches, onChanged: onChanged}
}

// ClientCompatStreamChanged reports whether a wrapped stream mutated any event.
// Non-wrapped streams return false.
func ClientCompatStreamChanged(s streams.Stream[*httpclient.StreamEvent]) bool {
	if cs, ok := s.(*clientCompatStream); ok {
		return cs.changed
	}
	return false
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

	// Record message ids from output_item.done before (or regardless of) patching.
	if strings.EqualFold(cp.Type, "response.output_item.done") ||
		(cp.Type == "" && bytes.Contains(cp.Data, []byte(`"output_item.done"`))) {
		s.recordOutputItemDone(cp.Data)
	}

	should := ShouldPatchStreamEventType(cp.Type) ||
		(len(cp.Data) > 0 && (bytes.Contains(cp.Data, []byte("output_text")) ||
			bytes.Contains(cp.Data, []byte(`"type":"message"`)) ||
			bytes.Contains(cp.Data, []byte(`"type": "message"`)) ||
			bytes.Contains(cp.Data, []byte("conversation"))))

	if should {
		opts := &NormalizeOptions{}
		// Provide remaining hints as FIFO for missing message ids.
		for _, h := range s.messageHints {
			if h.Type == "message" && h.ID != "" {
				opts.MessageIDs = append(opts.MessageIDs, h.ID)
			}
		}
		if patched, ok := PatchStreamEventDataWithOpts(cp.Data, s.patches, opts); ok {
			cp.Data = patched
			if !s.changed {
				s.changed = true
				if s.onChanged != nil {
					s.onChanged()
					s.onChanged = nil
				}
			}
		}
	}
	s.current = &cp
	return true
}

func (s *clientCompatStream) recordOutputItemDone(data []byte) {
	if len(data) == 0 {
		return
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return
	}
	item, _ := root["item"].(map[string]any)
	if item == nil {
		return
	}
	typ, _ := item["type"].(string)
	if typ != "message" {
		return
	}
	id, _ := item["id"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	role, _ := item["role"].(string)
	// Avoid duplicates if the same item is emitted twice.
	for _, h := range s.messageHints {
		if h.ID == id {
			return
		}
	}
	s.messageHints = append(s.messageHints, streamItemHint{ID: id, Type: typ, Role: role})
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
