package biz

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectClient_ExplicitHeaderPriority(t *testing.T) {
	settings := DefaultClientCompatSettings()
	headers := http.Header{}
	headers.Set("User-Agent", "codex-tui/0.1.0")
	headers.Set("X-AxonHub-Client", "grok-build")
	headers.Set("X-AxonHub-Client-Version", "0.2.117")

	result := DetectClient(&settings, headers)
	assert.Equal(t, ClientProfileGrokBuild, result.ProfileID)
	assert.Equal(t, ClientDetectSourceExplicit, result.Source)
	assert.Equal(t, "high", result.Confidence)
	assert.Equal(t, "0.2.117", result.ClientVersion)
	assert.Equal(t, "Grok Build", result.DisplayName)
}

func TestDetectClient_ExplicitUnknownStillRecords(t *testing.T) {
	settings := DefaultClientCompatSettings()
	headers := http.Header{}
	headers.Set("X-AxonHub-Client", "my-custom-agent")

	result := DetectClient(&settings, headers)
	assert.Equal(t, ClientProfileUnknown, result.ProfileID)
	assert.Equal(t, ClientDetectSourceExplicit, result.Source)
	assert.Equal(t, "my-custom-agent", result.RawProfileID)
	assert.Equal(t, "medium", result.Confidence)
}

func TestDetectClient_GrokBuildUA(t *testing.T) {
	settings := DefaultClientCompatSettings()
	headers := http.Header{}
	headers.Set("User-Agent", "grok-pager/1.0.0 grok-shell/0.2.117")

	result := DetectClient(&settings, headers)
	assert.Equal(t, ClientProfileGrokBuild, result.ProfileID)
	assert.Equal(t, ClientDetectSourceUserAgent, result.Source)
	assert.Contains(t, result.MatchedRules, "grok_pager")
}

func TestDetectClient_CodexUA(t *testing.T) {
	settings := DefaultClientCompatSettings()
	headers := http.Header{}
	headers.Set("User-Agent", "codex-tui/0.50.0")

	result := DetectClient(&settings, headers)
	assert.Equal(t, ClientProfileCodex, result.ProfileID)
	assert.Equal(t, ClientDetectSourceUserAgent, result.Source)
}

func TestDetectClient_MainstreamAgents(t *testing.T) {
	settings := DefaultClientCompatSettings()
	cases := []struct {
		ua      string
		profile string
	}{
		{"claude-cli/1.0.0", ClientProfileClaudeCode},
		{"Cursor/1.2.3", ClientProfileCursor},
		{"Windsurf/1.0", ClientProfileWindsurf},
		{"Cline/2.0", ClientProfileCline},
		{"aider/0.50.0", ClientProfileAider},
		{"Continue/1.0", ClientProfileContinue},
		{"opencode/1.0", ClientProfileOpenCode},
		{"GitHubCopilotChat/1.0", ClientProfileCopilot},
		{"Roo-Code/1.0", ClientProfileRooCode},
		{"zed/0.1", ClientProfileZed},
		{"openai-python/1.0", ClientProfileGenericOpenAISDK},
	}
	for _, tc := range cases {
		t.Run(tc.ua, func(t *testing.T) {
			h := http.Header{}
			h.Set("User-Agent", tc.ua)
			result := DetectClient(&settings, h)
			assert.Equal(t, tc.profile, result.ProfileID)
		})
	}
}

func TestDetectClient_NoMatch(t *testing.T) {
	settings := DefaultClientCompatSettings()
	headers := http.Header{}
	headers.Set("User-Agent", "curl/8.0")

	result := DetectClient(&settings, headers)
	assert.Equal(t, ClientProfileUnknown, result.ProfileID)
	assert.Equal(t, ClientDetectSourceNone, result.Source)
}

func TestEnsureOutputTextAnnotations_FillsMissing(t *testing.T) {
	input := []byte(`{
		"type":"response.completed",
		"response":{
			"output":[{
				"type":"message",
				"content":[
					{"type":"output_text","text":"hello"},
					{"type":"output_text","text":"world","annotations":[{"type":"url_citation"}]}
				]
			}]
		}
	}`)

	out, changed := EnsureOutputTextAnnotations(input)
	require.True(t, changed)

	var root map[string]any
	require.NoError(t, json.Unmarshal(out, &root))
	resp := root["response"].(map[string]any)
	output := resp["output"].([]any)
	msg := output[0].(map[string]any)
	content := msg["content"].([]any)

	first := content[0].(map[string]any)
	assert.NotNil(t, first["annotations"])
	anns, ok := first["annotations"].([]any)
	require.True(t, ok)
	assert.Len(t, anns, 0)

	second := content[1].(map[string]any)
	secAnns := second["annotations"].([]any)
	assert.Len(t, secAnns, 1)
}

func TestEnsureOutputTextAnnotations_NullToEmpty(t *testing.T) {
	input := []byte(`{"part":{"type":"output_text","text":"x","annotations":null}}`)
	out, changed := EnsureOutputTextAnnotations(input)
	require.True(t, changed)
	assert.Contains(t, string(out), `"annotations":[]`)
	assert.NotContains(t, string(out), `"annotations":null`)
}

func TestEnsureOutputTextAnnotations_NoChangeWhenPresent(t *testing.T) {
	input := []byte(`{"type":"output_text","text":"x","annotations":[]}`)
	_, changed := EnsureOutputTextAnnotations(input)
	assert.False(t, changed)
}

func TestEnsureOutputTextAnnotations_SkipsNonJSON(t *testing.T) {
	input := []byte(`[DONE]`)
	out, changed := EnsureOutputTextAnnotations(input)
	assert.False(t, changed)
	assert.Equal(t, input, out)
}

func TestShouldPatchResponse_DefaultsOff(t *testing.T) {
	settings := DefaultClientCompatSettings()
	detect := &ClientDetectResult{ProfileID: ClientProfileGrokBuild}
	assert.False(t, ShouldPatchResponse(&settings, detect))

	settings.Enabled = true
	assert.False(t, ShouldPatchResponse(&settings, detect), "profile still disabled")

	p := settings.Profiles[ClientProfileGrokBuild]
	p.Enabled = true
	settings.Profiles[ClientProfileGrokBuild] = p
	assert.True(t, ShouldPatchResponse(&settings, detect))
}

func TestCompareJSONAgainstTemplate_MissingAnnotations(t *testing.T) {
	tpl := defaultClientTemplates()[TemplateGrokBuildResponsesV1]
	input := []byte(`{
		"response":{
			"output":[{
				"type":"message",
				"content":[{"type":"output_text","text":"hi"}]
			}]
		}
	}`)
	result, err := CompareJSONAgainstTemplate(input, tpl)
	require.NoError(t, err)
	require.NotEmpty(t, result.MissingPaths)
	require.NotEmpty(t, result.AbsentVsEmptyArrays)
}

func TestCompareJSONAgainstTemplate_OK(t *testing.T) {
	tpl := defaultClientTemplates()[TemplateGrokBuildResponsesV1]
	input := []byte(`{
		"response":{
			"output":[{
				"type":"message",
				"content":[{"type":"output_text","text":"hi","annotations":[]}]
			}]
		}
	}`)
	result, err := CompareJSONAgainstTemplate(input, tpl)
	require.NoError(t, err)
	assert.Empty(t, result.MissingPaths)
	assert.Empty(t, result.AbsentVsEmptyArrays)
}

func TestNormalizeProfileID_Aliases(t *testing.T) {
	assert.Equal(t, ClientProfileGrokBuild, normalizeProfileID("grok-build"))
	assert.Equal(t, ClientProfileGrokBuild, normalizeProfileID("GrokBuild"))
	assert.Equal(t, ClientProfileClaudeCode, normalizeProfileID("claude-code"))
	assert.Equal(t, ClientProfileRooCode, normalizeProfileID("roo"))
}
