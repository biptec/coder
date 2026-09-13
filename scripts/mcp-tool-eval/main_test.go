package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCurrentDeveloperCatalog(t *testing.T) {
	t.Parallel()

	tools, err := currentDeveloperCatalog(t.Context())
	require.NoError(t, err)
	require.Len(t, tools, 25)

	names := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		names[tool.Name] = struct{}{}
	}
	for _, name := range []string{
		"capabilities",
		"exec",
		"read_file",
		"process_start",
		"process_output",
		"recent_activity",
	} {
		_, ok := names[name]
		require.True(t, ok, "expected developer catalog tool %q", name)
	}
}

func TestDefaultCandidateCatalog(t *testing.T) {
	t.Parallel()

	baseline, err := currentDeveloperCatalog(t.Context())
	require.NoError(t, err)
	overlay, err := loadCandidateOverlay("")
	require.NoError(t, err)
	candidate, err := applyOverlay(baseline, overlay)
	require.NoError(t, err)
	require.Len(t, candidate, len(baseline)+7)

	for _, name := range []string{
		"remote_hosts",
		"copy_path",
		"remove_path",
		"http_fetch",
		"http_request",
		"code_query",
		"code_rename",
	} {
		require.NotNil(t, findTool(candidate, name), "expected candidate tool %q", name)
		require.Nil(t, findTool(baseline, name), "candidate tool %q leaked into baseline", name)
	}

	baselineRead := findTool(baseline, "read_file")
	candidateRead := findTool(candidate, "read_file")
	require.NotNil(t, baselineRead)
	require.NotNil(t, candidateRead)
	require.NotContains(t, baselineRead.Parameters["properties"], "host")
	require.Contains(t, candidateRead.Parameters["properties"], "host")
	require.Equal(t, toolAnnotations{ReadOnly: true, Destructive: false, Idempotent: true, OpenWorld: false}, baselineRead.Annotations)
	require.Equal(t, toolAnnotations{ReadOnly: true, Destructive: false, Idempotent: true, OpenWorld: true}, candidateRead.Annotations)

	require.Equal(t, toolAnnotations{ReadOnly: true, Destructive: false, Idempotent: true, OpenWorld: false}, findTool(candidate, "remote_hosts").Annotations)
	require.Equal(t, toolAnnotations{ReadOnly: true, Destructive: false, Idempotent: true, OpenWorld: true}, findTool(candidate, "http_fetch").Annotations)
	require.Equal(t, toolAnnotations{ReadOnly: false, Destructive: true, Idempotent: false, OpenWorld: true}, findTool(candidate, "http_request").Annotations)
	require.Equal(t, toolAnnotations{ReadOnly: false, Destructive: true, Idempotent: false, OpenWorld: true}, findTool(candidate, "remove_path").Annotations)
	require.Equal(t, toolAnnotations{ReadOnly: true, Destructive: false, Idempotent: true, OpenWorld: false}, findTool(candidate, "code_query").Annotations)
	require.Equal(t, toolAnnotations{ReadOnly: false, Destructive: true, Idempotent: false, OpenWorld: false}, findTool(candidate, "code_rename").Annotations)
}

func TestDefaultSpecializedCatalog(t *testing.T) {
	t.Parallel()

	baseline, err := currentDeveloperCatalog(t.Context())
	require.NoError(t, err)
	candidateOverlay, err := loadCandidateOverlay("")
	require.NoError(t, err)
	shared, err := applyOverlay(baseline, catalogOverlay{Version: 1, AddTools: candidateOverlay.AddTools})
	require.NoError(t, err)
	specializedOverlay, err := loadSpecializedOverlay("")
	require.NoError(t, err)
	specialized, err := applyOverlay(shared, specializedOverlay)
	require.NoError(t, err)
	require.Len(t, specialized, len(baseline)+27)

	localRead := findTool(specialized, "read_file")
	remoteRead := findTool(specialized, "remote_read_file")
	require.NotNil(t, localRead)
	require.NotNil(t, remoteRead)
	require.NotContains(t, localRead.Parameters["properties"], "host")
	require.Contains(t, remoteRead.Parameters["properties"], "host")
	require.Equal(t, toolAnnotations{ReadOnly: true, Destructive: false, Idempotent: true, OpenWorld: false}, localRead.Annotations)
	require.Equal(t, toolAnnotations{ReadOnly: true, Destructive: false, Idempotent: true, OpenWorld: true}, remoteRead.Annotations)

	localOutput := findTool(specialized, "process_output")
	remoteOutput := findTool(specialized, "remote_process_output")
	require.NotNil(t, localOutput)
	require.NotNil(t, remoteOutput)
	require.False(t, localOutput.Annotations.OpenWorld)
	require.True(t, remoteOutput.Annotations.OpenWorld)
}

func TestDefaultScenariosValidate(t *testing.T) {
	t.Parallel()

	baseline, err := currentDeveloperCatalog(t.Context())
	require.NoError(t, err)
	candidateOverlay, err := loadCandidateOverlay("")
	require.NoError(t, err)
	candidate, err := applyOverlay(baseline, candidateOverlay)
	require.NoError(t, err)
	shared, err := applyOverlay(baseline, catalogOverlay{Version: 1, AddTools: candidateOverlay.AddTools})
	require.NoError(t, err)
	specializedOverlay, err := loadSpecializedOverlay("")
	require.NoError(t, err)
	specialized, err := applyOverlay(shared, specializedOverlay)
	require.NoError(t, err)

	set, err := loadScenarioSet("")
	require.NoError(t, err)
	require.Len(t, set.Scenarios, 54)

	categories := map[string]int{}
	for _, sc := range set.Scenarios {
		categories[sc.Category]++
		require.NotEmpty(t, sc.Source, sc.ID)
	}
	require.NoError(t, validateScenarios(set, map[string][]toolSpec{
		"baseline":    baseline,
		"candidate":   candidate,
		"specialized": specialized,
	}))
	for _, category := range []string{"filesystem", "search", "workspace", "execution", "process", "remote", "http", "code"} {
		require.Positive(t, categories[category], "missing scenario category %q", category)
	}
}

func TestSchemaAdherenceRejectsUnsupportedRemoteArgument(t *testing.T) {
	t.Parallel()

	baseline, err := currentDeveloperCatalog(t.Context())
	require.NoError(t, err)
	overlay, err := loadCandidateOverlay("")
	require.NoError(t, err)
	candidate, err := applyOverlay(baseline, overlay)
	require.NoError(t, err)

	arguments := map[string]any{
		"workspace": "developer/coder",
		"host":      "edge-1",
		"path":      "/etc/caddy/Caddyfile",
	}
	require.False(t, argumentsAdhereToSchema(findTool(baseline, "read_file").Parameters, arguments))
	require.True(t, argumentsAdhereToSchema(findTool(candidate, "read_file").Parameters, arguments))
}

func TestScoreSelection(t *testing.T) {
	t.Parallel()

	tools := []toolSpec{
		{
			Name: "read_file",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"workspace": map[string]any{"type": "string"},
					"path":      map[string]any{"type": "string"},
				},
				"required": []any{"workspace", "path"},
			},
		},
	}
	sc := scenario{
		Expected: expectedCall{
			Tool: "read_file",
			Arguments: map[string]any{
				"workspace": "developer/coder",
				"path":      "/tmp/a",
			},
		},
		ForbiddenTools: []string{"bash"},
	}

	toolMatch, argsMatch, schemaValid, forbidden, passed := scoreSelection(sc, selection{
		Tool: "read_file",
		Arguments: map[string]any{
			"workspace": "developer/coder",
			"path":      "/tmp/a",
		},
	}, tools)
	require.True(t, toolMatch)
	require.True(t, argsMatch)
	require.True(t, schemaValid)
	require.False(t, forbidden)
	require.True(t, passed)

	toolMatch, argsMatch, schemaValid, forbidden, passed = scoreSelection(sc, selection{
		Tool: "read_file",
		Arguments: map[string]any{
			"workspace": "developer/coder",
			"path":      "/tmp/a",
			"host":      "edge-1",
		},
	}, tools)
	require.True(t, toolMatch)
	require.True(t, argsMatch)
	require.False(t, schemaValid)
	require.False(t, forbidden)
	require.False(t, passed)
}

func TestResponsesSelector(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/responses", r.URL.Path)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var request responsesRequest
		require.NoError(t, json.Unmarshal(body, &request))
		require.Equal(t, "test-model", request.Model)
		require.Equal(t, "required", request.ToolChoice)
		require.False(t, request.ParallelToolCalls)
		require.Len(t, request.Tools, 1)
		require.Equal(t, "read_file", request.Tools[0].Name)

		rw.Header().Set("Content-Type", "application/json")
		_, err = rw.Write([]byte(`{
			"output": [{
				"type": "function_call",
				"name": "read_file",
				"arguments": "{\"workspace\":\"developer/coder\",\"path\":\"/tmp/a\"}"
			}],
			"usage": {"input_tokens": 123, "output_tokens": 17, "total_tokens": 140}
		}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	selector := newResponsesSelector(server.URL, "test-key", "test-model")
	selected, usage, err := selector.Select(t.Context(), "Read /tmp/a", []toolSpec{{
		Name:        "read_file",
		Description: "Read a file",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"workspace": map[string]any{"type": "string"},
				"path":      map[string]any{"type": "string"},
			},
		},
	}})
	require.NoError(t, err)
	require.Equal(t, "read_file", selected.Tool)
	require.Equal(t, "developer/coder", selected.Arguments["workspace"])
	require.Equal(t, "/tmp/a", selected.Arguments["path"])
	require.EqualValues(t, 123, usage.InputTokens)
	require.EqualValues(t, 17, usage.OutputTokens)
	require.EqualValues(t, 140, usage.TotalTokens)
}

func TestResponsesSelectorRejectsAmbiguousOutput(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		_, err := rw.Write([]byte(`{
			"output": [
				{"type":"function_call","name":"exec","arguments":"{}"},
				{"type":"function_call","name":"bash","arguments":"{}"}
			]
		}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	selector := newResponsesSelector(server.URL, "", "test-model")
	_, _, err := selector.Select(t.Context(), "do something", []toolSpec{
		{Name: "exec", Parameters: map[string]any{"type": "object"}},
		{Name: "bash", Parameters: map[string]any{"type": "object"}},
	})
	require.ErrorContains(t, err, "expected exactly one function call")
}

func TestRunWritesComparativeReportWithoutExecutingTools(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var request responsesRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Contains(t, request.Input, "/home/coder/work/example/config.yaml")

		rw.Header().Set("Content-Type", "application/json")
		_, err := rw.Write([]byte(`{
			"output": [{
				"type": "function_call",
				"name": "read_file",
				"arguments": "{\"workspace\":\"developer/coder\",\"path\":\"/home/coder/work/example/config.yaml\"}"
			}],
			"usage": {"input_tokens": 100, "output_tokens": 10, "total_tokens": 110}
		}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	outputPath := t.TempDir() + "/report.json"
	err := run(t.Context(), []string{
		"--model", "test-model",
		"--base-url", server.URL,
		"--api-key-env=-",
		"--repeat", "1",
		"--filter", "local-read-file",
		"--output", outputPath,
	})
	require.NoError(t, err)

	data, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	var report runReport
	require.NoError(t, json.Unmarshal(data, &report))
	require.Equal(t, "test-model", report.Model)
	require.Len(t, report.Results, 3)
	for _, result := range report.Results {
		require.Equal(t, "local-read-file", result.ScenarioID)
		require.Equal(t, "read_file", result.ExpectedTool)
		require.Equal(t, "read_file", result.Selection.Tool)
		require.True(t, result.SchemaValid)
		require.True(t, result.Passed)
	}

	info, err := os.Stat(outputPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestApplyOverlayRejectsUnknownTool(t *testing.T) {
	t.Parallel()

	_, err := applyOverlay([]toolSpec{{Name: "known", Parameters: map[string]any{"type": "object"}}}, catalogOverlay{
		Version:    1,
		PatchTools: []toolPatch{{Name: "missing"}},
	})
	require.ErrorContains(t, err, `unknown tool "missing"`)
}
