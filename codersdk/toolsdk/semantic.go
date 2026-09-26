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
		Description: `Find language-level symbols by name with the semantic engine; prefer this over text search/grep for code navigation.

Use this as the default way to locate language-level symbols such as functions,
methods, types, interfaces, classes, variables, and constants. Prefer it over
start_search, grep, rg, or shell text search when the intent is code navigation
by symbol name.

This tool uses the workspace semantic engine rather than text search. root is
required and limits the semantic search scope. Results include a reusable
locator that can be passed directly to find_references or find_implementations.
If semantic coverage is unavailable or partial for the target, fall back to
start_search/read_file instead of treating an empty semantic result as proof
that the symbol does not exist.

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
		Description: `Find semantic usages and call sites for a symbol; prefer this for impact analysis over text search.

Use this as the default tool for usages, call sites, and impact analysis before
a refactor. Prefer it over grep, rg, or literal text search when the question is
"where is this symbol used?"

target uses an absolute file path plus 1-based line and Unicode-code-point
column. Pass find_symbol.locator directly when available.

This tool never falls back to text search. include_declaration defaults to
false. scope_path optionally filters semantic references to one file or
directory after semantic resolution. If semantic coverage is unavailable or
partial, use start_search as an explicit fallback.

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
		Description: `Find semantic interface, trait, and method implementations; prefer this over matching names in text.

Use this as the default tool for interface/trait implementations, concrete
implementers, and method implementations. Prefer it over searching for matching
type or method names in text.

target uses an absolute file path plus 1-based line and Unicode-code-point
column. Pass find_symbol.locator directly when available.

This tool never falls back to text search or type-name matching. If the selected
semantic backend does not support implementation lookup, the tool returns
capability_unsupported rather than an empty implementation list. In that case,
use start_search/read_file as an explicit fallback.

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
		Description: `Get language-aware diagnostics for edited supported source files; use build/test for project-wide validation.

Use this after editing supported source files when language-aware file-level
validation is useful, especially for type, syntax, or semantic errors. Prefer
it over invoking a language server manually. Do not force it onto docs, config,
or unsupported languages.

The Agent synchronizes current file contents with the semantic backend and
returns diagnostics associated with the requested files. A clean supported file
returns no diagnostics.

paths is explicit and file-oriented; build/test commands through start_process
remain authoritative for whole-project validation.

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
