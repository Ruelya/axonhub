package biz

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// CompareJSONAgainstTemplate compares a response (or fragment) JSON document against a template.
//
// Rules:
//   - RequiredPaths that start with "**." are treated as structural rules on matching objects
//     (currently: **.output_text.annotations → every type==output_text must have annotations key).
//   - Other required paths are simple top-level dotted lookups from root.
//   - AbsentVsEmptyArrayPaths reports when a required array-like field is missing entirely
//     (as opposed to present as []).
//   - ExtraPaths is best-effort for known output_text keys only (not a full OpenAPI diff).
func CompareJSONAgainstTemplate(data []byte, template ClientTemplate) (*ClientSchemaCompareResult, error) {
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	result := &ClientSchemaCompareResult{
		TemplateID:          template.ID,
		MissingPaths:        []string{},
		ExtraPaths:          []string{},
		TypeMismatches:      []string{},
		AbsentVsEmptyArrays: []string{},
	}

	// Structural: output_text.annotations
	needAnnotations := false
	for _, p := range template.RequiredPaths {
		if p == "**.output_text.annotations" || p == "output_text.annotations" {
			needAnnotations = true
		}
	}
	absentCheck := false
	for _, p := range template.AbsentVsEmptyArrayPaths {
		if p == "**.output_text.annotations" || p == "output_text.annotations" {
			absentCheck = true
		}
	}

	if needAnnotations || absentCheck {
		walkOutputText(root, "", func(path string, obj map[string]any) {
			ann, exists := obj["annotations"]
			fullPath := path + ".annotations"
			if !exists {
				if needAnnotations {
					result.MissingPaths = append(result.MissingPaths, fullPath)
				}
				if absentCheck {
					result.AbsentVsEmptyArrays = append(result.AbsentVsEmptyArrays, fullPath+" (key absent)")
				}
				return
			}
			if ann == nil {
				if needAnnotations {
					result.MissingPaths = append(result.MissingPaths, fullPath+" (null)")
				}
				if absentCheck {
					result.AbsentVsEmptyArrays = append(result.AbsentVsEmptyArrays, fullPath+" (null)")
				}
				return
			}
			switch ann.(type) {
			case []any:
				// ok, including empty array
			default:
				result.TypeMismatches = append(result.TypeMismatches, fullPath+" expected array")
			}

			// Extra keys on output_text beyond common OpenAI fields (informational).
			known := map[string]struct{}{
				"type": {}, "text": {}, "annotations": {}, "logprobs": {},
			}
			for k := range obj {
				if _, ok := known[k]; !ok {
					result.ExtraPaths = append(result.ExtraPaths, path+"."+k)
				}
			}
		})
	}

	// Simple dotted required paths (non-structural).
	for _, p := range template.RequiredPaths {
		if strings.HasPrefix(p, "**.") || p == "output_text.annotations" {
			continue
		}
		if !pathExists(root, p) {
			result.MissingPaths = append(result.MissingPaths, p)
		}
	}

	sort.Strings(result.MissingPaths)
	sort.Strings(result.ExtraPaths)
	sort.Strings(result.TypeMismatches)
	sort.Strings(result.AbsentVsEmptyArrays)
	return result, nil
}

// ApplySelectedPatches applies optional fill rules chosen by the UI (dry-run or real).
// When enabled, runs the full Grok-strict Responses suite (not annotations alone).
func ApplySelectedPatches(data []byte, ensureAnnotations bool) ([]byte, bool) {
	if !ensureAnnotations {
		return data, false
	}
	return NormalizeResponsesJSON(data, nil)
}

func walkOutputText(v any, path string, fn func(path string, obj map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		cur := path
		if typ, ok := x["type"].(string); ok && typ == "output_text" {
			if cur == "" {
				cur = "output_text"
			}
			fn(cur, x)
		}
		for k, child := range x {
			childPath := k
			if path != "" {
				childPath = path + "." + k
			}
			// Prefer indexed paths for content arrays later via []any branch.
			walkOutputText(child, childPath, fn)
		}
	case []any:
		for i, child := range x {
			childPath := fmt.Sprintf("%s[%d]", path, i)
			if path == "" {
				childPath = fmt.Sprintf("[%d]", i)
			}
			walkOutputText(child, childPath, fn)
		}
	}
}

func pathExists(root any, dotted string) bool {
	parts := strings.Split(dotted, ".")
	cur := root
	for _, p := range parts {
		// strip simple [n] suffixes for map navigation only
		key := p
		idx := -1
		if i := strings.Index(p, "["); i >= 0 && strings.HasSuffix(p, "]") {
			key = p[:i]
			fmt.Sscanf(p[i+1:len(p)-1], "%d", &idx)
		}
		if key != "" {
			m, ok := cur.(map[string]any)
			if !ok {
				return false
			}
			next, ok := m[key]
			if !ok {
				return false
			}
			cur = next
		}
		if idx >= 0 {
			arr, ok := cur.([]any)
			if !ok || idx >= len(arr) {
				return false
			}
			cur = arr[idx]
		}
	}
	return cur != nil
}
