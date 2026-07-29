package responses

import (
	"encoding/json"
	"strings"
	"fmt"

	"github.com/looplj/axonhub/llm"
)

func attachOpenAIResponsesRequestExtensions(chatReq *llm.Request, req *Request, rawBody []byte) {
	if chatReq == nil || req == nil {
		return
	}

	raw := parseRawRequestFragments(rawBody)
	reasoningContext := ""
	if req.Reasoning != nil {
		reasoningContext = req.Reasoning.Context
	}
	requestExt := &llm.OpenAIResponsesRequestExtensions{
		ReasoningContext: reasoningContext,
		RawTools:         buildRawOnlyToolFragments(req.Tools, raw.Tools),
		ToolSignatures:   buildRepresentedToolSignatures(req.Tools),
		RawToolChoice:    rawUnsupportedToolChoice(req.ToolChoice, raw.ToolChoice),
		RawInputItems:    buildRawOnlyInputFragments(req.Input, raw.InputItems),
	}

	if requestExt.ReasoningContext == "" && len(requestExt.RawTools) == 0 && len(requestExt.RawToolChoice) == 0 && len(requestExt.RawInputItems) == 0 {
		return
	}

	ext := llm.EnsureOpenAIResponsesProviderExtensions(chatReq)
	if ext == nil {
		return
	}
	ext.Request = requestExt
}

type rawRequestFragments struct {
	Tools      []json.RawMessage
	ToolChoice json.RawMessage
	InputItems []json.RawMessage
}

func parseRawRequestFragments(rawBody []byte) rawRequestFragments {
	if len(rawBody) == 0 {
		return rawRequestFragments{}
	}

	var raw struct {
		Tools      []json.RawMessage `json:"tools"`
		ToolChoice json.RawMessage   `json:"tool_choice"`
		Input      json.RawMessage   `json:"input"`
	}
	if err := json.Unmarshal(rawBody, &raw); err != nil {
		return rawRequestFragments{}
	}

	var inputItems []json.RawMessage
	if len(raw.Input) > 0 && json.Unmarshal(raw.Input, &inputItems) != nil {
		inputItems = nil
	}

	return rawRequestFragments{
		Tools:      raw.Tools,
		ToolChoice: raw.ToolChoice,
		InputItems: inputItems,
	}
}

func buildRepresentedToolSignatures(tools []Tool) []string {
	if len(tools) == 0 {
		return nil
	}

	signatures := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool.Type == "namespace" {
			for _, subTool := range tool.Tools {
				if subTool.Type == "function" {
					signatures = append(signatures, responseToolSignature(Tool{
						Type: "function",
						Name: namespaceFunctionName(tool.Name, subTool.Name),
					}))
				}
			}
			continue
		}
		if !isStructurallyRepresentedToolType(tool.Type) {
			continue
		}
		signatures = append(signatures, responseToolSignature(tool))
	}

	return signatures
}

func buildRawOnlyToolFragments(tools []Tool, rawTools []json.RawMessage) []llm.OpenAIResponsesRawFragment {
	if len(tools) == 0 {
		return nil
	}

	fragments := make([]llm.OpenAIResponsesRawFragment, 0, len(tools))
	for i := range tools {
		if i >= len(rawTools) || len(rawTools[i]) == 0 || isStructurallyRepresentedToolType(tools[i].Type) {
			continue
		}

		fragments = append(fragments, llm.OpenAIResponsesRawFragment{
			Type:                 tools[i].Type,
			Name:                 tools[i].Name,
			OriginalIndex:        i,
			RepresentedToolCount: representedNamespaceToolCount(tools[i]),
			Raw:                  cloneRaw(rawTools[i]),
		})
	}

	return fragments
}

func representedNamespaceToolCount(tool Tool) int {
	if tool.Type != "namespace" {
		return 0
	}

	count := 0
	for _, subTool := range tool.Tools {
		if subTool.Type == "function" {
			count++
		}
	}

	return count
}

func isStructurallyRepresentedToolType(toolType string) bool {
	switch toolType {
	case "function", "image_generation", "web_search", "custom":
		return true
	default:
		return false
	}
}

func responseToolSignature(tool Tool) string {
	switch tool.Type {
	case "function", "custom":
		return tool.Type + ":" + tool.Name
	default:
		return tool.Type
	}
}

func rawUnsupportedToolChoice(choice *ToolChoice, rawChoice json.RawMessage) json.RawMessage {
	if choice == nil || len(rawChoice) == 0 {
		return nil
	}

	if len(choice.Tools) > 0 {
		return cloneRaw(rawChoice)
	}

	return nil
}

func buildRawOnlyInputFragments(input Input, rawItems []json.RawMessage) []llm.OpenAIResponsesRawFragment {
	if len(input.Items) == 0 {
		return nil
	}

	fragments := make([]llm.OpenAIResponsesRawFragment, 0)
	for i := range input.Items {
		item := input.Items[i]
		if i >= len(rawItems) || len(rawItems[i]) == 0 || isStructurallyRepresentedInputItem(item.Type) {
			continue
		}

		fragments = append(fragments, llm.OpenAIResponsesRawFragment{
			Type:          item.Type,
			Name:          item.Name,
			CallID:        item.CallID,
			OriginalIndex: i,
			Raw:           cloneRaw(rawItems[i]),
		})
	}

	return fragments
}

func convertAgentMessageRawToUserMessage(raw json.RawMessage) (json.RawMessage, bool) {
	var am struct {
		ID        string `json:"id"`
		Author    string `json:"author"`
		Recipient string `json:"recipient"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &am); err != nil {
		return nil, false
	}
	var b strings.Builder
	if am.Author != "" || am.Recipient != "" {
		fmt.Fprintf(&b, "[Codex agent_message author=%s recipient=%s]\n", am.Author, am.Recipient)
	}
	for _, p := range am.Content {
		switch p.Type {
		case "input_text", "output_text", "text":
			if strings.TrimSpace(p.Text) != "" {
				b.WriteString(p.Text)
				if !strings.HasSuffix(p.Text, "\n") {
					b.WriteByte('\n')
				}
			}
		}
	}
	text := strings.TrimSpace(b.String())
	if text == "" {
		text = "[Codex agent_message with no plaintext content]"
	}
	msg := map[string]any{
		"type": "message",
		"role": "user",
		"content": []map[string]string{
			{"type": "input_text", "text": text},
		},
	}
	if am.ID != "" {
		msg["id"] = am.ID
	}
	out, err := json.Marshal(msg)
	if err != nil {
		return nil, false
	}
	return out, true
}

func isStructurallyRepresentedInputItem(itemType string) bool {
	switch itemType {
	case "", "message", "input_text", "input_image", "function_call", "function_call_output",
		"custom_tool_call", "custom_tool_call_output", "reasoning", "compaction", "compaction_summary",
		// Converted to a plain user message in inbound convertItemToMessage — must NOT be
		// re-injected as a raw Codex-only fragment (xAI/CPA reject agent_message → 422 ModelInput).
		"agent_message":
		return true
	default:
		return false
	}
}
func openAIResponsesRequestExtensions(llmReq *llm.Request) *llm.OpenAIResponsesRequestExtensions {
	if llmReq == nil || llmReq.ProviderExtensions == nil || llmReq.ProviderExtensions.OpenAIResponses == nil {
		return nil
	}
	requestExt := llmReq.ProviderExtensions.OpenAIResponses.Request

	return requestExt
}

func marshalRequestPayload(payload Request, llmReq *llm.Request) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	requestExt := openAIResponsesRequestExtensions(llmReq)
	if requestExt == nil {
		return body, nil
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}

	if tools, ok := mergeRawOnlyTools(obj["tools"], requestExt); ok {
		toolsRaw, err := json.Marshal(tools)
		if err != nil {
			return nil, err
		}
		obj["tools"] = toolsRaw
	}

	if len(requestExt.RawToolChoice) > 0 && rawToolChoiceMatchesCurrentTools(requestExt.RawToolChoice, payload.ToolChoice) {
		obj["tool_choice"] = cloneRaw(requestExt.RawToolChoice)
	}

	if input, ok := mergeRawOnlyInputItems(obj["input"], requestExt); ok {
		inputRaw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		obj["input"] = inputRaw
	}

	return json.Marshal(obj)
}

func mergeRawOnlyInputItems(structuredRaw json.RawMessage, requestExt *llm.OpenAIResponsesRequestExtensions) ([]json.RawMessage, bool) {
	if requestExt == nil || len(requestExt.RawInputItems) == 0 {
		return nil, false
	}

	var structuredItems []json.RawMessage
	if len(structuredRaw) > 0 {
		if err := json.Unmarshal(structuredRaw, &structuredItems); err != nil {
			return nil, false
		}
	}

	total := len(structuredItems) + len(requestExt.RawInputItems)
	items := make([]json.RawMessage, 0, total)
	structuredIndex := 0
	rawByIndex := make(map[int]json.RawMessage, len(requestExt.RawInputItems))
	for _, fragment := range requestExt.RawInputItems {
		if len(fragment.Raw) == 0 || fragment.OriginalIndex < 0 {
			return nil, false
		}
		rawByIndex[fragment.OriginalIndex] = cloneRaw(fragment.Raw)
	}

	for i := 0; i < total; i++ {
		if raw, ok := rawByIndex[i]; ok {
			// Defensive: never forward Codex-only agent_message to non-native upstreams.
			var peek struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(raw, &peek)
			if peek.Type == "agent_message" {
				if converted, cok := convertAgentMessageRawToUserMessage(raw); cok {
					raw = converted
				}
			}
			items = append(items, raw)
			continue
		}
		if structuredIndex >= len(structuredItems) {
			return nil, false
		}
		items = append(items, cloneRaw(structuredItems[structuredIndex]))
		structuredIndex++
	}

	if structuredIndex != len(structuredItems) {
		return nil, false
	}

	return items, true
}

func mergeRawOnlyTools(structuredRaw json.RawMessage, requestExt *llm.OpenAIResponsesRequestExtensions) ([]json.RawMessage, bool) {
	if requestExt == nil || len(requestExt.RawTools) == 0 {
		return nil, false
	}

	var structuredTools []json.RawMessage
	if len(structuredRaw) > 0 {
		if err := json.Unmarshal(structuredRaw, &structuredTools); err != nil {
			return nil, false
		}
	}
	if !structuredToolSignaturesMatch(structuredTools, requestExt.ToolSignatures) {
		return nil, false
	}

	representedCount := 0
	for _, fragment := range requestExt.RawTools {
		if fragment.RepresentedToolCount < 0 {
			return nil, false
		}
		representedCount += fragment.RepresentedToolCount
	}
	if representedCount > len(structuredTools) {
		return nil, false
	}

	total := len(structuredTools) - representedCount + len(requestExt.RawTools)
	tools := make([]json.RawMessage, 0, total)
	structuredIndex := 0
	rawByIndex := make(map[int]llm.OpenAIResponsesRawFragment, len(requestExt.RawTools))
	for _, fragment := range requestExt.RawTools {
		if len(fragment.Raw) == 0 || fragment.OriginalIndex < 0 {
			return nil, false
		}
		rawByIndex[fragment.OriginalIndex] = fragment
	}

	for i := 0; i < total; i++ {
		if fragment, ok := rawByIndex[i]; ok {
			tools = append(tools, cloneRaw(fragment.Raw))
			structuredIndex += fragment.RepresentedToolCount
			if structuredIndex > len(structuredTools) {
				return nil, false
			}
			continue
		}
		if structuredIndex >= len(structuredTools) {
			return nil, false
		}
		tools = append(tools, cloneRaw(structuredTools[structuredIndex]))
		structuredIndex++
	}

	if structuredIndex != len(structuredTools) {
		return nil, false
	}

	return tools, true
}

func structuredToolSignaturesMatch(structuredTools []json.RawMessage, expected []string) bool {
	if len(structuredTools) != len(expected) {
		return false
	}

	for i, rawTool := range structuredTools {
		var tool Tool
		if err := json.Unmarshal(rawTool, &tool); err != nil {
			return false
		}
		if responseToolSignature(tool) != expected[i] {
			return false
		}
	}

	return true
}

func rawToolChoiceMatchesCurrentTools(raw json.RawMessage, current *ToolChoice) bool {
	if current == nil {
		return true
	}

	var rawChoice ToolChoice
	if err := json.Unmarshal(raw, &rawChoice); err != nil {
		return false
	}

	currentSignature := toolChoiceSignature(current)
	if currentSignature == "" {
		return true
	}

	return toolChoiceSignature(&rawChoice) == currentSignature
}

func toolChoiceSignature(choice *ToolChoice) string {
	if choice == nil {
		return ""
	}

	if choice.Mode != nil {
		return "mode:" + *choice.Mode
	}

	if choice.Type != nil && choice.Name != nil {
		return "named:" + *choice.Type + ":" + *choice.Name
	}

	if len(choice.Tools) > 0 {
		return "tools"
	}

	return ""
}

func cloneRaw(src json.RawMessage) json.RawMessage {
	if len(src) == 0 {
		return nil
	}

	return append(json.RawMessage(nil), src...)
}
