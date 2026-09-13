package main

import "encoding/json"

type toolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  map[string]any  `json:"parameters"`
	Annotations toolAnnotations `json:"annotations"`
}

type toolAnnotations struct {
	ReadOnly    bool `json:"read_only"`
	Destructive bool `json:"destructive"`
	Idempotent  bool `json:"idempotent"`
	OpenWorld   bool `json:"open_world"`
}

type toolAnnotationPatch struct {
	ReadOnly    *bool `json:"read_only,omitempty"`
	Destructive *bool `json:"destructive,omitempty"`
	Idempotent  *bool `json:"idempotent,omitempty"`
	OpenWorld   *bool `json:"open_world,omitempty"`
}

type toolPatch struct {
	Name              string               `json:"name"`
	DescriptionAppend string               `json:"description_append,omitempty"`
	AddProperties     map[string]any       `json:"add_properties,omitempty"`
	AddRequired       []string             `json:"add_required,omitempty"`
	Annotations       *toolAnnotationPatch `json:"annotations,omitempty"`
}

type toolClone struct {
	Source            string               `json:"source"`
	Name              string               `json:"name"`
	Description       string               `json:"description,omitempty"`
	DescriptionAppend string               `json:"description_append,omitempty"`
	AddProperties     map[string]any       `json:"add_properties,omitempty"`
	AddRequired       []string             `json:"add_required,omitempty"`
	Annotations       *toolAnnotationPatch `json:"annotations,omitempty"`
}

type catalogOverlay struct {
	Version    int         `json:"version"`
	PatchTools []toolPatch `json:"patch_tools,omitempty"`
	CloneTools []toolClone `json:"clone_tools,omitempty"`
	AddTools   []toolSpec  `json:"add_tools,omitempty"`
}

type scenarioSet struct {
	Version   int        `json:"version"`
	Scenarios []scenario `json:"scenarios"`
}

type scenario struct {
	ID                string                  `json:"id"`
	Category          string                  `json:"category"`
	Source            string                  `json:"source,omitempty"`
	Prompt            string                  `json:"prompt"`
	Expected          expectedCall            `json:"expected"`
	ExpectedByCatalog map[string]expectedCall `json:"expected_by_catalog,omitempty"`
	ForbiddenTools    []string                `json:"forbidden_tools,omitempty"`
	Weight            float64                 `json:"weight,omitempty"`
	Notes             string                  `json:"notes,omitempty"`
}

type expectedCall struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type selection struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments,omitempty"`
	RawArgs   string         `json:"raw_arguments,omitempty"`
}

type tokenUsage struct {
	InputTokens  int64 `json:"input_tokens,omitempty"`
	OutputTokens int64 `json:"output_tokens,omitempty"`
	TotalTokens  int64 `json:"total_tokens,omitempty"`
}

type runResult struct {
	Catalog        string     `json:"catalog"`
	ScenarioID     string     `json:"scenario_id"`
	Category       string     `json:"category"`
	ExpectedTool   string     `json:"expected_tool"`
	Iteration      int        `json:"iteration"`
	Selection      selection  `json:"selection"`
	ToolMatch      bool       `json:"tool_match"`
	ArgumentsMatch bool       `json:"arguments_match"`
	SchemaValid    bool       `json:"schema_valid"`
	Forbidden      bool       `json:"forbidden"`
	Passed         bool       `json:"passed"`
	Usage          tokenUsage `json:"usage,omitempty"`
	LatencyMS      int64      `json:"latency_ms"`
	Error          string     `json:"error,omitempty"`
}

type runReport struct {
	GeneratedAt string                 `json:"generated_at"`
	Model       string                 `json:"model"`
	Repeats     int                    `json:"repeats"`
	Catalogs    map[string]catalogInfo `json:"catalogs"`
	Results     []runResult            `json:"results"`
}

type catalogInfo struct {
	ToolCount   int `json:"tool_count"`
	SchemaBytes int `json:"schema_bytes"`
}

func cloneJSON[T any](value T) (T, error) {
	var out T
	data, err := json.Marshal(value)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	return out, nil
}
