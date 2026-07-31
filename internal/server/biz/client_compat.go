package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/looplj/axonhub/internal/ent"
)

const (
	// SystemKeyClientCompat is the system KV key for client compatibility settings.
	// The value is JSON-encoded ClientCompatSettings.
	SystemKeyClientCompat = "system_client_compat"

	// Default explicit client identification headers.
	DefaultClientHeader        = "X-AxonHub-Client"
	DefaultClientVersionHeader = "X-AxonHub-Client-Version"

	// Known profile IDs.
	ClientProfileGrokBuild          = "grok_build"
	ClientProfileCodex              = "codex"
	ClientProfileClaudeCode         = "claude_code"
	ClientProfileCursor             = "cursor"
	ClientProfileWindsurf           = "windsurf"
	ClientProfileCline              = "cline"
	ClientProfileAider              = "aider"
	ClientProfileContinue           = "continue"
	ClientProfileOpenCode           = "opencode"
	ClientProfileCopilot            = "copilot"
	ClientProfileRooCode            = "roo_code"
	ClientProfileZed                = "zed"
	ClientProfileGenericOpenAISDK   = "generic_openai_sdk"
	ClientProfileUnknown            = "unknown"

	// Built-in template IDs.
	TemplateGenericOpenAIResponses = "generic_openai_responses"
	TemplateGrokBuildResponsesV1   = "grok_build_responses_v1"

	// Detection sources.
	ClientDetectSourceExplicit  = "explicit"
	ClientDetectSourceUserAgent = "user_agent"
	ClientDetectSourceNone      = "none"
)

// ClientCompatSettings is the full client-compat configuration stored in system KV.
type ClientCompatSettings struct {
	// Enabled is the master switch for runtime response patching.
	// Detection for UI badges works independently from request headers.
	Enabled bool `json:"enabled"`

	// Detection holds header names and UA rules.
	Detection ClientDetectionConfig `json:"detection"`

	// Profiles maps profile id → profile config.
	Profiles map[string]ClientProfileConfig `json:"profiles"`

	// Templates holds built-in / user template metadata and schema rules.
	// Built-in templates are merged on read; user overrides may clone them later.
	Templates map[string]ClientTemplate `json:"templates"`
}

// ClientDetectionConfig configures how clients are identified.
type ClientDetectionConfig struct {
	ExplicitHeader        string            `json:"explicitHeader"`
	ExplicitVersionHeader string            `json:"explicitVersionHeader"`
	UserAgentRules        []ClientUARule    `json:"userAgentRules"`
}

// ClientUARule matches a User-Agent substring/regex to a profile.
type ClientUARule struct {
	ID        string `json:"id"`
	Pattern   string `json:"pattern"`
	ProfileID string `json:"profileId"`
	Priority  int    `json:"priority"`
	Enabled   bool   `json:"enabled"`
	// IsRegex when true treats Pattern as a regular expression (case-insensitive).
	IsRegex bool `json:"isRegex,omitempty"`
}

// ClientProfileConfig controls patches for a detected client profile.
type ClientProfileConfig struct {
	DisplayName string            `json:"displayName"`
	Enabled     bool              `json:"enabled"`
	TemplateID  string            `json:"templateId"`
	Patches     ClientPatchConfig `json:"patches"`
}

// ClientPatchConfig lists known response wire patches.
type ClientPatchConfig struct {
	// EnsureOutputTextAnnotations fills missing/null annotations on output_text parts with [].
	EnsureOutputTextAnnotations bool `json:"ensureOutputTextAnnotations"`
}

// ClientTemplate describes expected API shape for a client (schema subset).
type ClientTemplate struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"displayName"`
	Description string   `json:"description"`
	// RequiredPaths are dotted JSON paths that should exist (e.g. output_text.annotations).
	// Special path "**.output_text.annotations" means any object with type==output_text must have annotations.
	RequiredPaths []string `json:"requiredPaths"`
	// AbsentVsEmptyArrayPaths treats missing key vs [] as a distinct mismatch class.
	AbsentVsEmptyArrayPaths []string `json:"absentVsEmptyArrayPaths"`
	// Builtin marks templates shipped with AxonHub (not user-editable in place).
	Builtin bool `json:"builtin"`
}

// ClientDetectResult is the outcome of client identification.
type ClientDetectResult struct {
	ProfileID     string   `json:"profileId"`
	DisplayName   string   `json:"displayName"`
	Source        string   `json:"source"`
	Confidence    string   `json:"confidence"` // high | medium | low | none
	UserAgent     string   `json:"userAgent,omitempty"`
	ClientHeader  string   `json:"clientHeader,omitempty"`
	ClientVersion string   `json:"clientVersion,omitempty"`
	MatchedRules  []string `json:"matchedRules,omitempty"`
	RawProfileID  string   `json:"rawProfileId,omitempty"` // original explicit header value when unknown
}

// ClientSchemaCompareResult holds missing/extra/type diffs against a template.
type ClientSchemaCompareResult struct {
	TemplateID          string   `json:"templateId"`
	MissingPaths        []string `json:"missingPaths"`
	ExtraPaths          []string `json:"extraPaths"`
	TypeMismatches      []string `json:"typeMismatches"`
	AbsentVsEmptyArrays []string `json:"absentVsEmptyArrays"`
}

// ClientCompatPatchPreview is a dry-run patch result for the admin UI.
type ClientCompatPatchPreview struct {
	Changed     bool                       `json:"changed"`
	PatchedJSON string                     `json:"patchedJson"`
	Compare     *ClientSchemaCompareResult `json:"compare,omitempty"`
}

// ClientProfileView is the GraphQL list element for a profile (includes id).
type ClientProfileView struct {
	ID          string            `json:"id"`
	DisplayName string            `json:"displayName"`
	Enabled     bool              `json:"enabled"`
	TemplateID  string            `json:"templateId"`
	Patches     ClientPatchConfig `json:"patches"`
}

// ClientCompatView is the GraphQL-facing settings shape (lists instead of maps).
type ClientCompatView struct {
	Enabled   bool                   `json:"enabled"`
	Detection ClientDetectionConfig  `json:"detection"`
	Profiles  []ClientProfileView    `json:"profiles"`
	Templates []ClientTemplate       `json:"templates"`
}

// ClientCompatUpdate is a partial update applied by SystemService.
type ClientCompatUpdate struct {
	Enabled               *bool
	ExplicitHeader        *string
	ExplicitVersionHeader *string
	UserAgentRules        []ClientUARule
	Profiles              []ClientProfileUpdate
}

// ClientProfileUpdate updates a single profile by id.
type ClientProfileUpdate struct {
	ID                          string
	Enabled                     *bool
	TemplateID                  *string
	EnsureOutputTextAnnotations *bool
}

// ToView converts stored settings into the GraphQL list view.
func (s ClientCompatSettings) ToView() ClientCompatView {
	NormalizeClientCompatSettings(&s)
	profiles := make([]ClientProfileView, 0, len(s.Profiles))
	// Stable order: grok_build first, then alpha.
	ids := make([]string, 0, len(s.Profiles))
	for id := range s.Profiles {
		ids = append(ids, id)
	}
	sort.SliceStable(ids, func(i, j int) bool {
		if ids[i] == ClientProfileGrokBuild {
			return true
		}
		if ids[j] == ClientProfileGrokBuild {
			return false
		}
		return ids[i] < ids[j]
	})
	for _, id := range ids {
		p := s.Profiles[id]
		profiles = append(profiles, ClientProfileView{
			ID:          id,
			DisplayName: p.DisplayName,
			Enabled:     p.Enabled,
			TemplateID:  p.TemplateID,
			Patches:     p.Patches,
		})
	}
	templates := make([]ClientTemplate, 0, len(s.Templates))
	tids := make([]string, 0, len(s.Templates))
	for id := range s.Templates {
		tids = append(tids, id)
	}
	sort.Strings(tids)
	for _, id := range tids {
		templates = append(templates, s.Templates[id])
	}
	return ClientCompatView{
		Enabled:   s.Enabled,
		Detection: s.Detection,
		Profiles:  profiles,
		Templates: templates,
	}
}

type clientDetectContextKey struct{}

// WithClientDetect stores the detect result on the request context.
func WithClientDetect(ctx context.Context, result *ClientDetectResult) context.Context {
	if result == nil {
		return ctx
	}
	return context.WithValue(ctx, clientDetectContextKey{}, result)
}

// GetClientDetect retrieves the detect result from context.
func GetClientDetect(ctx context.Context) (*ClientDetectResult, bool) {
	v, ok := ctx.Value(clientDetectContextKey{}).(*ClientDetectResult)
	return v, ok && v != nil
}

// DefaultClientCompatSettings returns factory defaults: detection rules on, all patches off.
func DefaultClientCompatSettings() ClientCompatSettings {
	return ClientCompatSettings{
		Enabled: false,
		Detection: ClientDetectionConfig{
			ExplicitHeader:        DefaultClientHeader,
			ExplicitVersionHeader: DefaultClientVersionHeader,
			UserAgentRules:        defaultClientUARules(),
		},
		Profiles:  defaultClientProfiles(),
		Templates: defaultClientTemplates(),
	}
}

func defaultClientUARules() []ClientUARule {
	// Higher priority wins on multi-match. Grok Build is prioritized above generic openai SDK.
	return []ClientUARule{
		{ID: "grok_pager", Pattern: "grok-pager/", ProfileID: ClientProfileGrokBuild, Priority: 100, Enabled: true},
		{ID: "grok_shell", Pattern: "grok-shell/", ProfileID: ClientProfileGrokBuild, Priority: 100, Enabled: true},
		{ID: "grok_build", Pattern: "grok-build", ProfileID: ClientProfileGrokBuild, Priority: 90, Enabled: true},

		{ID: "codex_tui", Pattern: "codex-tui/", ProfileID: ClientProfileCodex, Priority: 100, Enabled: true},
		{ID: "codex_cli", Pattern: "codex-cli/", ProfileID: ClientProfileCodex, Priority: 100, Enabled: true},
		{ID: "codex", Pattern: "codex/", ProfileID: ClientProfileCodex, Priority: 80, Enabled: true},

		{ID: "claude_cli", Pattern: "claude-cli/", ProfileID: ClientProfileClaudeCode, Priority: 100, Enabled: true},
		{ID: "claude_code", Pattern: "claude-code", ProfileID: ClientProfileClaudeCode, Priority: 90, Enabled: true},
		{ID: "anthropic_claude_code", Pattern: "@anthropic-ai/claude-code", ProfileID: ClientProfileClaudeCode, Priority: 90, Enabled: true},

		{ID: "cursor_slash", Pattern: "Cursor/", ProfileID: ClientProfileCursor, Priority: 100, Enabled: true},
		{ID: "cursor_dash", Pattern: "cursor-", ProfileID: ClientProfileCursor, Priority: 80, Enabled: true},

		{ID: "windsurf_slash", Pattern: "Windsurf/", ProfileID: ClientProfileWindsurf, Priority: 100, Enabled: true},
		{ID: "windsurf", Pattern: "windsurf", ProfileID: ClientProfileWindsurf, Priority: 70, Enabled: true},

		{ID: "cline_slash", Pattern: "Cline/", ProfileID: ClientProfileCline, Priority: 100, Enabled: true},
		{ID: "cline", Pattern: "cline", ProfileID: ClientProfileCline, Priority: 70, Enabled: true},

		{ID: "aider_slash", Pattern: "aider/", ProfileID: ClientProfileAider, Priority: 100, Enabled: true},
		{ID: "aider", Pattern: "Aider", ProfileID: ClientProfileAider, Priority: 80, Enabled: true},

		{ID: "continue_slash", Pattern: "Continue/", ProfileID: ClientProfileContinue, Priority: 100, Enabled: true},
		{ID: "continue_dev", Pattern: "continue.dev", ProfileID: ClientProfileContinue, Priority: 90, Enabled: true},

		{ID: "opencode", Pattern: "opencode", ProfileID: ClientProfileOpenCode, Priority: 80, Enabled: true},
		{ID: "opencode_title", Pattern: "OpenCode", ProfileID: ClientProfileOpenCode, Priority: 80, Enabled: true},

		{ID: "copilot_chat", Pattern: "GitHubCopilotChat/", ProfileID: ClientProfileCopilot, Priority: 100, Enabled: true},
		{ID: "copilot", Pattern: "copilot", ProfileID: ClientProfileCopilot, Priority: 60, Enabled: true},

		{ID: "roo_code", Pattern: "Roo-Code", ProfileID: ClientProfileRooCode, Priority: 100, Enabled: true},
		{ID: "roo_code_dash", Pattern: "roo-code", ProfileID: ClientProfileRooCode, Priority: 90, Enabled: true},

		{ID: "zed_slash", Pattern: "zed/", ProfileID: ClientProfileZed, Priority: 100, Enabled: true},
		{ID: "zed", Pattern: "Zed", ProfileID: ClientProfileZed, Priority: 80, Enabled: true},

		{ID: "openai_node", Pattern: "openai-node", ProfileID: ClientProfileGenericOpenAISDK, Priority: 40, Enabled: true},
		{ID: "openai_python", Pattern: "openai-python", ProfileID: ClientProfileGenericOpenAISDK, Priority: 40, Enabled: true},
		{ID: "openai_slash", Pattern: "OpenAI/", ProfileID: ClientProfileGenericOpenAISDK, Priority: 30, Enabled: true},
	}
}

func defaultClientProfiles() map[string]ClientProfileConfig {
	return map[string]ClientProfileConfig{
		ClientProfileGrokBuild: {
			DisplayName: "Grok Build",
			Enabled:     false,
			TemplateID:  TemplateGrokBuildResponsesV1,
			Patches: ClientPatchConfig{
				EnsureOutputTextAnnotations: true, // preferred patch when profile is enabled; profile itself defaults off
			},
		},
		ClientProfileCodex: {
			DisplayName: "Codex",
			Enabled:     false,
			TemplateID:  TemplateGenericOpenAIResponses,
			Patches:     ClientPatchConfig{},
		},
		ClientProfileClaudeCode: {
			DisplayName: "Claude Code",
			Enabled:     false,
			TemplateID:  TemplateGenericOpenAIResponses,
			Patches:     ClientPatchConfig{},
		},
		ClientProfileCursor: {
			DisplayName: "Cursor",
			Enabled:     false,
			TemplateID:  TemplateGenericOpenAIResponses,
			Patches:     ClientPatchConfig{},
		},
		ClientProfileWindsurf: {
			DisplayName: "Windsurf",
			Enabled:     false,
			TemplateID:  TemplateGenericOpenAIResponses,
			Patches:     ClientPatchConfig{},
		},
		ClientProfileCline: {
			DisplayName: "Cline",
			Enabled:     false,
			TemplateID:  TemplateGenericOpenAIResponses,
			Patches:     ClientPatchConfig{},
		},
		ClientProfileAider: {
			DisplayName: "Aider",
			Enabled:     false,
			TemplateID:  TemplateGenericOpenAIResponses,
			Patches:     ClientPatchConfig{},
		},
		ClientProfileContinue: {
			DisplayName: "Continue",
			Enabled:     false,
			TemplateID:  TemplateGenericOpenAIResponses,
			Patches:     ClientPatchConfig{},
		},
		ClientProfileOpenCode: {
			DisplayName: "OpenCode",
			Enabled:     false,
			TemplateID:  TemplateGenericOpenAIResponses,
			Patches:     ClientPatchConfig{},
		},
		ClientProfileCopilot: {
			DisplayName: "GitHub Copilot",
			Enabled:     false,
			TemplateID:  TemplateGenericOpenAIResponses,
			Patches:     ClientPatchConfig{},
		},
		ClientProfileRooCode: {
			DisplayName: "Roo Code",
			Enabled:     false,
			TemplateID:  TemplateGenericOpenAIResponses,
			Patches:     ClientPatchConfig{},
		},
		ClientProfileZed: {
			DisplayName: "Zed",
			Enabled:     false,
			TemplateID:  TemplateGenericOpenAIResponses,
			Patches:     ClientPatchConfig{},
		},
		ClientProfileGenericOpenAISDK: {
			DisplayName: "OpenAI SDK",
			Enabled:     false,
			TemplateID:  TemplateGenericOpenAIResponses,
			Patches:     ClientPatchConfig{},
		},
		ClientProfileUnknown: {
			DisplayName: "Unknown",
			Enabled:     false,
			TemplateID:  TemplateGenericOpenAIResponses,
			Patches:     ClientPatchConfig{},
		},
	}
}

func defaultClientTemplates() map[string]ClientTemplate {
	return map[string]ClientTemplate{
		TemplateGenericOpenAIResponses: {
			ID:          TemplateGenericOpenAIResponses,
			DisplayName: "OpenAI Responses (generic)",
			Description: "Baseline OpenAI Responses shape. Empty annotations may be omitted (omitzero).",
			RequiredPaths: []string{
				"response.output",
			},
			AbsentVsEmptyArrayPaths: []string{
				"**.output_text.annotations",
			},
			Builtin: true,
		},
		TemplateGrokBuildResponsesV1: {
			ID:          TemplateGrokBuildResponsesV1,
			DisplayName: "Grok Build Responses v1",
			Description: "Grok Build strict schema: output_text.annotations must be present (even when empty).",
			RequiredPaths: []string{
				"response.output",
				"**.output_text.annotations",
			},
			AbsentVsEmptyArrayPaths: []string{
				"**.output_text.annotations",
			},
			Builtin: true,
		},
	}
}

// NormalizeClientCompatSettings fills defaults and merges built-in templates/rules.
func NormalizeClientCompatSettings(settings *ClientCompatSettings) {
	if settings == nil {
		return
	}

	def := DefaultClientCompatSettings()

	if settings.Detection.ExplicitHeader == "" {
		settings.Detection.ExplicitHeader = def.Detection.ExplicitHeader
	}
	if settings.Detection.ExplicitVersionHeader == "" {
		settings.Detection.ExplicitVersionHeader = def.Detection.ExplicitVersionHeader
	}
	if len(settings.Detection.UserAgentRules) == 0 {
		settings.Detection.UserAgentRules = def.Detection.UserAgentRules
	}

	if settings.Profiles == nil {
		settings.Profiles = map[string]ClientProfileConfig{}
	}
	for id, p := range def.Profiles {
		if existing, ok := settings.Profiles[id]; ok {
			if existing.DisplayName == "" {
				existing.DisplayName = p.DisplayName
			}
			if existing.TemplateID == "" {
				existing.TemplateID = p.TemplateID
			}
			settings.Profiles[id] = existing
		} else {
			settings.Profiles[id] = p
		}
	}

	if settings.Templates == nil {
		settings.Templates = map[string]ClientTemplate{}
	}
	// Built-in templates always win for id/description/required paths (users may clone later).
	for id, t := range def.Templates {
		settings.Templates[id] = t
	}
}

// ClientCompatSettings retrieves client-compat settings (or defaults).
func (s *SystemService) ClientCompatSettings(ctx context.Context) (*ClientCompatSettings, error) {
	value, err := s.getSystemValue(ctx, SystemKeyClientCompat)
	if err != nil {
		if ent.IsNotFound(err) {
			settings := DefaultClientCompatSettings()
			return &settings, nil
		}
		return nil, fmt.Errorf("failed to get client compat settings: %w", err)
	}

	var settings ClientCompatSettings
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return nil, fmt.Errorf("failed to unmarshal client compat settings: %w", err)
	}
	NormalizeClientCompatSettings(&settings)
	return &settings, nil
}

// ClientCompatSettingsOrDefault returns settings or factory defaults on error.
func (s *SystemService) ClientCompatSettingsOrDefault(ctx context.Context) *ClientCompatSettings {
	settings, err := s.ClientCompatSettings(ctx)
	if err != nil {
		def := DefaultClientCompatSettings()
		return &def
	}
	return settings
}

// SetClientCompatSettings stores the full client-compat settings JSON.
func (s *SystemService) SetClientCompatSettings(ctx context.Context, settings ClientCompatSettings) error {
	NormalizeClientCompatSettings(&settings)

	// Never persist built-in template bodies as mutable user content beyond the map keys we allow;
	// still store templates so custom ones can be added later. Built-ins are re-merged on read.
	jsonBytes, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("failed to marshal client compat settings: %w", err)
	}
	return s.setSystemValue(ctx, SystemKeyClientCompat, string(jsonBytes))
}

// ApplyClientCompatUpdate merges a partial update into current settings.
func (s *SystemService) ApplyClientCompatUpdate(ctx context.Context, input ClientCompatUpdate) error {
	current, err := s.ClientCompatSettings(ctx)
	if err != nil {
		return err
	}

	if input.Enabled != nil {
		current.Enabled = *input.Enabled
	}
	if input.ExplicitHeader != nil {
		h := strings.TrimSpace(*input.ExplicitHeader)
		if h != "" {
			current.Detection.ExplicitHeader = h
		}
	}
	if input.ExplicitVersionHeader != nil {
		h := strings.TrimSpace(*input.ExplicitVersionHeader)
		if h != "" {
			current.Detection.ExplicitVersionHeader = h
		}
	}
	if input.UserAgentRules != nil {
		current.Detection.UserAgentRules = input.UserAgentRules
	}
	for _, p := range input.Profiles {
		id := strings.TrimSpace(p.ID)
		if id == "" {
			continue
		}
		existing, ok := current.Profiles[id]
		if !ok {
			existing = ClientProfileConfig{
				DisplayName: id,
				TemplateID:  TemplateGenericOpenAIResponses,
			}
		}
		if p.Enabled != nil {
			existing.Enabled = *p.Enabled
		}
		if p.TemplateID != nil && strings.TrimSpace(*p.TemplateID) != "" {
			existing.TemplateID = strings.TrimSpace(*p.TemplateID)
		}
		if p.EnsureOutputTextAnnotations != nil {
			existing.Patches.EnsureOutputTextAnnotations = *p.EnsureOutputTextAnnotations
		}
		current.Profiles[id] = existing
	}

	return s.SetClientCompatSettings(ctx, *current)
}

// DetectClient identifies the client from headers (explicit header first, then UA).
func DetectClient(settings *ClientCompatSettings, headers http.Header) ClientDetectResult {
	if settings == nil {
		def := DefaultClientCompatSettings()
		settings = &def
	}
	NormalizeClientCompatSettings(settings)

	ua := headers.Get("User-Agent")
	explicitHeaderName := settings.Detection.ExplicitHeader
	if explicitHeaderName == "" {
		explicitHeaderName = DefaultClientHeader
	}
	versionHeaderName := settings.Detection.ExplicitVersionHeader
	if versionHeaderName == "" {
		versionHeaderName = DefaultClientVersionHeader
	}

	clientHeader := strings.TrimSpace(headers.Get(explicitHeaderName))
	clientVersion := strings.TrimSpace(headers.Get(versionHeaderName))

	result := ClientDetectResult{
		ProfileID:     ClientProfileUnknown,
		DisplayName:   profileDisplayName(settings, ClientProfileUnknown),
		Source:        ClientDetectSourceNone,
		Confidence:    "none",
		UserAgent:     ua,
		ClientHeader:  clientHeader,
		ClientVersion: clientVersion,
	}

	// 1) Explicit header wins.
	if clientHeader != "" {
		normalized := normalizeProfileID(clientHeader)
		result.RawProfileID = clientHeader
		result.Source = ClientDetectSourceExplicit
		if _, known := settings.Profiles[normalized]; known && normalized != ClientProfileUnknown {
			result.ProfileID = normalized
			result.DisplayName = profileDisplayName(settings, normalized)
			result.Confidence = "high"
			return result
		}
		// Unknown explicit id: still mark as explicit/unknown for UI.
		result.ProfileID = ClientProfileUnknown
		result.DisplayName = profileDisplayName(settings, ClientProfileUnknown)
		result.Confidence = "medium"
		return result
	}

	// 2) User-Agent rules.
	if ua == "" {
		return result
	}

	type match struct {
		rule ClientUARule
	}
	var matches []match
	for _, rule := range settings.Detection.UserAgentRules {
		if !rule.Enabled || rule.Pattern == "" || rule.ProfileID == "" {
			continue
		}
		if matchUARule(ua, rule) {
			matches = append(matches, match{rule: rule})
		}
	}
	if len(matches) == 0 {
		return result
	}

	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].rule.Priority > matches[j].rule.Priority
	})

	best := matches[0].rule
	matchedIDs := make([]string, 0, len(matches))
	for _, m := range matches {
		matchedIDs = append(matchedIDs, m.rule.ID)
	}

	result.ProfileID = normalizeProfileID(best.ProfileID)
	result.DisplayName = profileDisplayName(settings, result.ProfileID)
	result.Source = ClientDetectSourceUserAgent
	result.Confidence = "high"
	result.MatchedRules = matchedIDs
	return result
}

// DetectClientFromMaps is a convenience wrapper for header maps (e.g. stored requestHeaders JSON).
func DetectClientFromMaps(settings *ClientCompatSettings, headers map[string]any) ClientDetectResult {
	h := http.Header{}
	for k, v := range headers {
		switch t := v.(type) {
		case string:
			h.Set(k, t)
		case []any:
			for _, item := range t {
				if s, ok := item.(string); ok {
					h.Add(k, s)
				}
			}
		case []string:
			for _, s := range t {
				h.Add(k, s)
			}
		}
	}
	return DetectClient(settings, h)
}

func matchUARule(ua string, rule ClientUARule) bool {
	if rule.IsRegex {
		re, err := regexp.Compile("(?i)" + rule.Pattern)
		if err != nil {
			return false
		}
		return re.MatchString(ua)
	}
	return strings.Contains(strings.ToLower(ua), strings.ToLower(rule.Pattern))
}

func normalizeProfileID(id string) string {
	id = strings.TrimSpace(strings.ToLower(id))
	id = strings.ReplaceAll(id, "-", "_")
	id = strings.ReplaceAll(id, " ", "_")
	// Common aliases
	switch id {
	case "grokbuild", "grok":
		return ClientProfileGrokBuild
	case "claude", "claudecode":
		return ClientProfileClaudeCode
	case "roo", "roocode":
		return ClientProfileRooCode
	case "github_copilot", "githubcopilot":
		return ClientProfileCopilot
	case "openai", "openai_sdk":
		return ClientProfileGenericOpenAISDK
	}
	return id
}

func profileDisplayName(settings *ClientCompatSettings, profileID string) string {
	if settings != nil {
		if p, ok := settings.Profiles[profileID]; ok && p.DisplayName != "" {
			return p.DisplayName
		}
	}
	def := defaultClientProfiles()
	if p, ok := def[profileID]; ok {
		return p.DisplayName
	}
	return profileID
}

// ShouldPatchResponse reports whether wire-level patches should run for this detect result.
func ShouldPatchResponse(settings *ClientCompatSettings, detect *ClientDetectResult) bool {
	if settings == nil || detect == nil || !settings.Enabled {
		return false
	}
	profile, ok := settings.Profiles[detect.ProfileID]
	if !ok || !profile.Enabled {
		return false
	}
	return profile.Patches.EnsureOutputTextAnnotations
}

// ActivePatches returns the patch config for the detected profile when patching is active.
func ActivePatches(settings *ClientCompatSettings, detect *ClientDetectResult) (ClientPatchConfig, bool) {
	if !ShouldPatchResponse(settings, detect) {
		return ClientPatchConfig{}, false
	}
	return settings.Profiles[detect.ProfileID].Patches, true
}
