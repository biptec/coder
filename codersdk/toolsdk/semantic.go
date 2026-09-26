package toolsdk

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/coder/aisdk-go"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

var semanticSymbolKinds = []string{
	"file", "module", "namespace", "package", "class", "method",
	"property", "field", "constructor", "enum", "interface",
	"function", "variable", "constant", "string", "number",
	"boolean", "array", "object", "key", "null", "enum_member",
	"struct", "event", "operator", "type_parameter", "trait",
	"macro", "type", "unknown",
}

func semanticTargetSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":   map[string]any{"type": "string", "minLength": 1},
			"line":   map[string]any{"type": "integer", "minimum": 1},
			"column": map[string]any{"type": "integer", "minimum": 1},
		},
		"required": []string{"path", "line", "column"},
	}
}

func semanticContextLinesSchema(description string) map[string]any {
	return map[string]any{
		"type":        "integer",
		"minimum":     0,
		"maximum":     10,
		"description": description,
	}
}

type WorkspaceFindSymbolArgs struct {
	Workspace    string   `json:"workspace"`
	Root         string   `json:"root"`
	Query        string   `json:"query"`
	Match        string   `json:"match,omitempty"`
	Kinds        []string `json:"kinds,omitempty"`
	Limit        int      `json:"limit"`
	ContextLines int      `json:"context_lines,omitempty"`
}

var WorkspaceFindSymbol = Tool[WorkspaceFindSymbolArgs, workspacesdk.SemanticFindSymbolsResponse]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceFindSymbol,
		Description: `Find semantic code symbols by name; prefer this over text search for supported source navigation.

Prefer this over grep/rg or text search when the task is language-aware symbol
navigation in supported source code. Use start_search for arbitrary text,
filenames, configuration, documentation, generated files, or unsupported
language content.

This tool uses the workspace semantic engine rather than text search. root is
required and limits the semantic search scope. Results include a reusable
locator that can be passed directly to find_references or find_implementations.

limit is required: use 0 for all logical results available from the semantic
backend, or a positive value to bound the returned records. context_lines
optionally includes bounded source context around each symbol.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"root": map[string]any{
					"type": "string", "minLength": 1,
					"description": "Required absolute file or directory that scopes semantic symbol search.",
				},
				"query": map[string]any{
					"type": "string", "minLength": 1,
					"description": "Symbol name text to match.",
				},
				"match": map[string]any{
					"type": "string", "enum": []string{"exact", "prefix", "substring"},
					"description": "Name matching rule. Defaults to exact.",
				},
				"kinds": map[string]any{
					"type": "array", "minItems": 1, "uniqueItems": true,
					"items":       map[string]any{"type": "string", "enum": semanticSymbolKinds},
					"description": "Optional normalized symbol-kind filter. Omit to allow all kinds.",
				},
				"limit": map[string]any{
					"type": "integer", "minimum": 0,
					"description": "Required result limit. Use 0 for all logical results available from the semantic backend.",
				},
				"context_lines": semanticContextLinesSchema("Optional source context lines before and after each symbol. Defaults to 0."),
			},
			Required: []string{"workspace", "root", "query", "limit"},
		},
	},
	MCPAnnotations: semanticReadOnlyAnnotations,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceFindSymbolArgs) (workspacesdk.SemanticFindSymbolsResponse, error) {
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return workspacesdk.SemanticFindSymbolsResponse{}, err
		}
		defer conn.Close()
		resp, err := conn.FindSemanticSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
			Root: args.Root, Query: args.Query, Match: args.Match, Kinds: args.Kinds,
			Limit: args.Limit, ContextLines: args.ContextLines,
		})
		if err != nil {
			return workspacesdk.SemanticFindSymbolsResponse{}, xerrors.Errorf("find semantic symbols: %w", err)
		}
		return resp, nil
	},
}

type WorkspaceFindReferencesArgs struct {
	Workspace          string                      `json:"workspace"`
	Target             workspacesdk.SemanticTarget `json:"target"`
	IncludeDeclaration bool                        `json:"include_declaration,omitempty"`
	ScopePath          string                      `json:"scope_path,omitempty"`
	Limit              int                         `json:"limit"`
	ContextLines       int                         `json:"context_lines,omitempty"`
}

var WorkspaceFindReferences = Tool[WorkspaceFindReferencesArgs, workspacesdk.SemanticFindReferencesResponse]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceFindReferences,
		Description: `Find semantic references at an exact symbol position; prefer this for refactor impact analysis over text search.

Use this as the default impact-analysis tool before refactoring a resolved
symbol; prefer it over text search when you need actual language references.

target uses an absolute file path plus 1-based line and Unicode-code-point
column. Pass find_symbol.locator directly when available.

This tool never falls back to text search. include_declaration defaults to
false. scope_path optionally filters semantic references to one file or
directory after semantic resolution.

limit is required: use 0 for all logical references returned by the semantic
backend, or a positive value to bound the returned records.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"target":    semanticTargetSchema(),
				"include_declaration": map[string]any{
					"type":        "boolean",
					"description": "Include the symbol declaration/definition when the backend supports that distinction. Defaults to false.",
				},
				"scope_path": map[string]any{
					"type": "string", "minLength": 1,
					"description": "Optional absolute file or directory filter applied to semantic results.",
				},
				"limit": map[string]any{
					"type": "integer", "minimum": 0,
					"description": "Required result limit. Use 0 for all logical references returned by the semantic backend.",
				},
				"context_lines": semanticContextLinesSchema("Optional source context lines before and after each reference. Defaults to 0."),
			},
			Required: []string{"workspace", "target", "limit"},
		},
	},
	MCPAnnotations: semanticReadOnlyAnnotations,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceFindReferencesArgs) (workspacesdk.SemanticFindReferencesResponse, error) {
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return workspacesdk.SemanticFindReferencesResponse{}, err
		}
		defer conn.Close()
		resp, err := conn.FindSemanticReferences(ctx, workspacesdk.SemanticFindReferencesRequest{
			Target: args.Target, IncludeDeclaration: args.IncludeDeclaration,
			ScopePath: args.ScopePath, Limit: args.Limit, ContextLines: args.ContextLines,
		})
		if err != nil {
			return workspacesdk.SemanticFindReferencesResponse{}, xerrors.Errorf("find semantic references: %w", err)
		}
		return resp, nil
	},
}

type WorkspaceFindImplementationsArgs struct {
	Workspace    string                      `json:"workspace"`
	Target       workspacesdk.SemanticTarget `json:"target"`
	ScopePath    string                      `json:"scope_path,omitempty"`
	Limit        int                         `json:"limit"`
	ContextLines int                         `json:"context_lines,omitempty"`
}

var WorkspaceFindImplementations = Tool[WorkspaceFindImplementationsArgs, workspacesdk.SemanticFindImplementationsResponse]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceFindImplementations,
		Description: `Find semantic implementations at an exact symbol position; prefer this over text/type-name search for implementations.

Prefer this over text or type-name search when you need concrete
implementations of an interface, trait, abstract/base symbol, or other
language construct understood by the semantic backend.

target uses an absolute file path plus 1-based line and Unicode-code-point
column. Pass find_symbol.locator directly when available.

This tool never falls back to text search or type-name matching. If the selected
semantic backend does not support implementation lookup, the tool returns
capability_unsupported rather than an empty implementation list.

scope_path optionally filters semantic implementation locations to one file or
directory. limit is required: use 0 for all logical implementations returned by
the semantic backend, or a positive value to bound the returned records.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"target":    semanticTargetSchema(),
				"scope_path": map[string]any{
					"type": "string", "minLength": 1,
					"description": "Optional absolute file or directory filter applied after semantic implementation lookup.",
				},
				"limit": map[string]any{
					"type": "integer", "minimum": 0,
					"description": "Required result limit. Use 0 for all logical implementations returned by the semantic backend.",
				},
				"context_lines": semanticContextLinesSchema("Optional source context lines before and after each implementation. Defaults to 0."),
			},
			Required: []string{"workspace", "target", "limit"},
		},
	},
	MCPAnnotations: semanticReadOnlyAnnotations,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceFindImplementationsArgs) (workspacesdk.SemanticFindImplementationsResponse, error) {
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return workspacesdk.SemanticFindImplementationsResponse{}, err
		}
		defer conn.Close()
		resp, err := conn.FindSemanticImplementations(ctx, workspacesdk.SemanticFindImplementationsRequest{
			Target: args.Target, ScopePath: args.ScopePath, Limit: args.Limit,
			ContextLines: args.ContextLines,
		})
		if err != nil {
			return workspacesdk.SemanticFindImplementationsResponse{}, xerrors.Errorf("find semantic implementations: %w", err)
		}
		return resp, nil
	},
}

type WorkspaceGetDiagnosticsArgs struct {
	Workspace                 string   `json:"workspace"`
	Paths                     []string `json:"paths"`
	MinimumSeverity           string   `json:"minimum_severity,omitempty"`
	IncludeRelatedInformation bool     `json:"include_related_information,omitempty"`
	Limit                     int      `json:"limit"`
	ContextLines              int      `json:"context_lines,omitempty"`
}

var WorkspaceGetDiagnostics = Tool[WorkspaceGetDiagnosticsArgs, workspacesdk.SemanticDiagnosticsResponse]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceGetDiagnostics,
		Description: `Get semantic diagnostics for explicit workspace files; prefer after supported-source edits while build/test remains authoritative.

The Agent synchronizes current file contents with the semantic backend and
returns diagnostics associated with the requested files. A clean supported file
returns no diagnostics.

Prefer this after editing supported source files when language-aware validation
is useful. It complements rather than replaces project build/test validation.
paths is explicit and file-oriented; use build/test commands through
start_process for authoritative whole-project validation.

limit is required and applies to the total returned diagnostic records across
all requested files.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"paths": map[string]any{
					"type": "array", "minItems": 1, "maxItems": 100, "uniqueItems": true,
					"items":       map[string]any{"type": "string", "minLength": 1},
					"description": "Absolute regular-file paths to analyze.",
				},
				"minimum_severity": map[string]any{
					"type": "string", "enum": []string{"error", "warning", "information", "hint"},
					"description": "Lowest severity to return. Defaults to hint.",
				},
				"include_related_information": map[string]any{
					"type":        "boolean",
					"description": "Include backend-provided related diagnostic locations/messages. Defaults to false.",
				},
				"limit": map[string]any{
					"type": "integer", "minimum": 0,
					"description": "Required total diagnostic result limit. Use 0 for all logical diagnostics returned for the requested files.",
				},
				"context_lines": semanticContextLinesSchema("Optional source context lines before and after each diagnostic. Defaults to 0."),
			},
			Required: []string{"workspace", "paths", "limit"},
		},
	},
	MCPAnnotations: semanticReadOnlyAnnotations,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceGetDiagnosticsArgs) (workspacesdk.SemanticDiagnosticsResponse, error) {
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return workspacesdk.SemanticDiagnosticsResponse{}, err
		}
		defer conn.Close()
		resp, err := conn.GetSemanticDiagnostics(ctx, workspacesdk.SemanticDiagnosticsRequest{
			Paths: args.Paths, MinimumSeverity: args.MinimumSeverity,
			IncludeRelatedInformation: args.IncludeRelatedInformation,
			Limit:                     args.Limit, ContextLines: args.ContextLines,
		})
		if err != nil {
			return workspacesdk.SemanticDiagnosticsResponse{}, xerrors.Errorf("get semantic diagnostics: %w", err)
		}
		return resp, nil
	},
}
