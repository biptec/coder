package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcptransport "github.com/mark3labs/mcp-go/client/transport"
	mcpproto "github.com/mark3labs/mcp-go/mcp"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	codermcp "github.com/coder/coder/v2/coderd/mcp"
	"github.com/coder/coder/v2/codersdk"
)

func currentDeveloperCatalog(ctx context.Context) ([]toolSpec, error) {
	baseURL, err := url.Parse("http://127.0.0.1")
	if err != nil {
		return nil, xerrors.Errorf("parse dummy coder URL: %w", err)
	}

	coderClient := codersdk.New(baseURL)
	mcpServer, err := codermcp.NewServer(slog.Make())
	if err != nil {
		return nil, xerrors.Errorf("create MCP server: %w", err)
	}
	mcpServer.SetActivityStore(codermcp.NewActivityStore(100), "tool-eval")
	if err := mcpServer.RegisterDeveloperTools(coderClient); err != nil {
		return nil, xerrors.Errorf("register developer tools: %w", err)
	}

	httpServer := httptest.NewServer(mcpServer)
	defer httpServer.Close()

	basicClient := isolatedHTTPClient()
	client, err := mcpclient.NewStreamableHttpClient(httpServer.URL, mcptransport.WithHTTPBasicClient(basicClient))
	if err != nil {
		return nil, xerrors.Errorf("create MCP client: %w", err)
	}
	defer func() { _ = client.Close() }()
	if transport, ok := basicClient.Transport.(*http.Transport); ok {
		defer transport.CloseIdleConnections()
	}

	catalogCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Start(catalogCtx); err != nil {
		return nil, xerrors.Errorf("start MCP client: %w", err)
	}
	_, err = client.Initialize(catalogCtx, mcpproto.InitializeRequest{Params: mcpproto.InitializeParams{
		ProtocolVersion: mcpproto.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcpproto.Implementation{Name: "mcp-tool-eval", Version: "1"},
	}})
	if err != nil {
		return nil, xerrors.Errorf("initialize MCP client: %w", err)
	}

	listed, err := client.ListTools(catalogCtx, mcpproto.ListToolsRequest{})
	if err != nil {
		return nil, xerrors.Errorf("list MCP tools: %w", err)
	}

	tools := make([]toolSpec, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		parameters, err := toolParameters(tool)
		if err != nil {
			return nil, xerrors.Errorf("normalize tool %q schema: %w", tool.Name, err)
		}
		tools = append(tools, toolSpec{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  parameters,
			Annotations: toolAnnotations{
				ReadOnly:    boolHint(tool.Annotations.ReadOnlyHint),
				Destructive: boolHint(tool.Annotations.DestructiveHint),
				Idempotent:  boolHint(tool.Annotations.IdempotentHint),
				OpenWorld:   boolHint(tool.Annotations.OpenWorldHint),
			},
		})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools, nil
}

func isolatedHTTPClient() *http.Client {
	if transport, ok := http.DefaultTransport.(*http.Transport); ok {
		return &http.Client{Transport: transport.Clone()}
	}
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}
}

func toolParameters(tool mcpproto.Tool) (map[string]any, error) {
	var raw []byte
	var err error
	if len(tool.RawInputSchema) > 0 {
		raw = tool.RawInputSchema
	} else {
		raw, err = json.Marshal(tool.InputSchema)
		if err != nil {
			return nil, err
		}
	}

	parameters := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &parameters); err != nil {
			return nil, err
		}
	}
	if parameters["type"] == nil {
		parameters["type"] = "object"
	}
	return parameters, nil
}

func applyOverlay(base []toolSpec, overlay catalogOverlay) ([]toolSpec, error) {
	tools, err := cloneJSON(base)
	if err != nil {
		return nil, xerrors.Errorf("clone catalog: %w", err)
	}

	byName := make(map[string]int, len(tools))
	for i := range tools {
		byName[tools[i].Name] = i
	}

	for _, patch := range overlay.PatchTools {
		index, ok := byName[patch.Name]
		if !ok {
			return nil, xerrors.Errorf("candidate patch references unknown tool %q", patch.Name)
		}
		if err := applyToolChanges(&tools[index], patch.DescriptionAppend, patch.AddProperties, patch.AddRequired, patch.Annotations); err != nil {
			return nil, xerrors.Errorf("patch tool %q: %w", patch.Name, err)
		}
	}

	for _, clone := range overlay.CloneTools {
		if clone.Name == "" {
			return nil, xerrors.New("cloned tool name cannot be empty")
		}
		if _, exists := byName[clone.Name]; exists {
			return nil, xerrors.Errorf("cloned tool %q already exists", clone.Name)
		}
		sourceIndex, ok := byName[clone.Source]
		if !ok {
			return nil, xerrors.Errorf("clone %q references unknown source tool %q", clone.Name, clone.Source)
		}
		cloned, err := cloneJSON(tools[sourceIndex])
		if err != nil {
			return nil, xerrors.Errorf("clone source tool %q: %w", clone.Source, err)
		}
		cloned.Name = clone.Name
		if clone.Description != "" {
			cloned.Description = clone.Description
		}
		if err := applyToolChanges(&cloned, clone.DescriptionAppend, clone.AddProperties, clone.AddRequired, clone.Annotations); err != nil {
			return nil, xerrors.Errorf("clone tool %q: %w", clone.Name, err)
		}
		tools = append(tools, cloned)
		byName[clone.Name] = len(tools) - 1
	}

	for _, added := range overlay.AddTools {
		if added.Name == "" {
			return nil, xerrors.New("candidate tool name cannot be empty")
		}
		if _, exists := byName[added.Name]; exists {
			return nil, xerrors.Errorf("candidate tool %q already exists", added.Name)
		}
		if added.Parameters == nil {
			added.Parameters = map[string]any{"type": "object"}
		}
		tools = append(tools, added)
		byName[added.Name] = len(tools) - 1
	}

	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools, nil
}

func applyToolChanges(tool *toolSpec, descriptionAppend string, addProperties map[string]any, addRequired []string, annotations *toolAnnotationPatch) error {
	if descriptionAppend != "" {
		if tool.Description != "" {
			tool.Description += "\n\n"
		}
		tool.Description += descriptionAppend
	}
	if len(addProperties) > 0 {
		properties, ok := tool.Parameters["properties"].(map[string]any)
		if !ok || properties == nil {
			properties = map[string]any{}
			tool.Parameters["properties"] = properties
		}
		for name, schema := range addProperties {
			if _, exists := properties[name]; exists {
				return xerrors.Errorf("property %q already exists", name)
			}
			properties[name] = schema
		}
	}
	if annotations != nil {
		applyAnnotationPatch(&tool.Annotations, *annotations)
	}
	if len(addRequired) > 0 {
		required := stringSlice(tool.Parameters["required"])
		seen := make(map[string]struct{}, len(required)+len(addRequired))
		for _, name := range required {
			seen[name] = struct{}{}
		}
		for _, name := range addRequired {
			if _, ok := seen[name]; ok {
				continue
			}
			required = append(required, name)
			seen[name] = struct{}{}
		}
		tool.Parameters["required"] = required
	}
	return nil
}

func boolHint(value *bool) bool {
	return value != nil && *value
}

func applyAnnotationPatch(current *toolAnnotations, patch toolAnnotationPatch) {
	if patch.ReadOnly != nil {
		current.ReadOnly = *patch.ReadOnly
	}
	if patch.Destructive != nil {
		current.Destructive = *patch.Destructive
	}
	if patch.Idempotent != nil {
		current.Idempotent = *patch.Idempotent
	}
	if patch.OpenWorld != nil {
		current.OpenWorld = *patch.OpenWorld
	}
}

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if value, ok := item.(string); ok {
				out = append(out, value)
			}
		}
		return out
	default:
		return nil
	}
}

func catalogStats(tools []toolSpec) (catalogInfo, error) {
	providerTools := make([]responsesTool, 0, len(tools))
	for _, tool := range tools {
		providerTools = append(providerTools, responsesTool{
			Type:        "function",
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.Parameters,
			Strict:      false,
		})
	}
	data, err := json.Marshal(providerTools)
	if err != nil {
		return catalogInfo{}, err
	}
	return catalogInfo{ToolCount: len(tools), SchemaBytes: len(data)}, nil
}
