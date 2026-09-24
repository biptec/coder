# Coder Remote MCP Semantic Code Tools Specification

Status: APPROVED IMPLEMENTATION SPECIFICATION - approved by human reviewer on 2026-09-23
Project: biptec/coder
Target branch family: custom/v2.35.3
Baseline public API: docs/mcp-public-api-spec-next.md
Approval date: 2026-09-23
Initial implementation scope: Go only, using the gopls already present in the Developer Workspace image
Primary contract rule: after approval, this document becomes the implementation contract for the four semantic code tools. Implementation must be reconciled against this document before PR review.

---

## 1. Purpose

This document specifies four new assistant-facing semantic code tools:

1. find_symbol
2. find_references
3. find_implementations
4. get_diagnostics

The tools are intended to add semantic code intelligence without turning language-server details into part of the public MCP API.

The initial implementation is deliberately minimal:

- Go is the only required language.
- The existing Developer Workspace image already contains gopls v0.21.0.
- No OCI image rebuild is required for the Go MVP.
- No additional language server is installed as part of the first implementation.
- Language servers for TypeScript, Python, Rust, C/C++, Java, and other languages are future work.
- The public schemas in this document are designed so those languages can be added later without changing the tool contracts.

Human approval was granted on 2026-09-23. Implementation is authorized for the Go MVP within the boundaries of this specification.

---

## 2. Goals

The semantic tools must make the common coding flow shorter and safer:

    locate a semantic symbol
        ->
    inspect references / implementations
        ->
    edit files with the existing filesystem tools
        ->
    request diagnostics for the changed files
        ->
    run normal build/tests with start_process when needed

The tools must:

- work from semantic information rather than grep heuristics;
- be language-neutral at the public MCP boundary;
- use precise source locations as the reusable identity between tools;
- be deterministic enough for an assistant to chain calls reliably;
- distinguish "no result" from "semantic capability unavailable";
- never silently fall back to text search while claiming semantic results;
- preserve the existing explicit-limit and response-safety conventions;
- be read-only with respect to user source files;
- reuse a persistent semantic backend instead of starting one process per MCP call;
- keep backend lifecycle independent from individual ChatGPT/MCP sessions.

---

## 3. Non-goals

This specification does not add:

- rename_symbol;
- code actions;
- formatting;
- completion;
- hover as a public tool;
- call hierarchy;
- type hierarchy;
- dependency installation;
- automatic package installation;
- automatic language-server installation;
- a generic raw LSP tool;
- a generic AST query tool;
- text-search fallback inside a semantic tool.

Existing tools remain responsible for:

- text/path search: start_search / get_search_results;
- file reads: read_file / read_multiple_files;
- edits: edit_file / edit_multiple_files / write_file;
- build, test, lint, git, package managers: start_process or execute_shell_command.

---

## 4. Research-derived design decisions

### 4.1 Semantic engine is an implementation detail

The public tools must not expose gopls, LSP method names, JetBrains PSI, or another backend-specific protocol as their contract.

Public API:

    find_references(...)

Internal implementation today:

    Semantic Manager -> gopls -> textDocument/references

Possible implementation later:

    Semantic Manager -> another language server
    Semantic Manager -> IDE semantic engine
    Semantic Manager -> indexed semantic service

Changing the backend must not require changing the public tool schema.

### 4.2 Names are for discovery; positions are for identity

find_symbol accepts a name query because the assistant is discovering a symbol.

find_references and find_implementations do not accept a symbol name. They accept a precise source target:

    {
      "path": "/absolute/path/file.go",
      "line": 123,
      "column": 17
    }

This avoids ambiguity from:

- overloaded methods;
- identical names in different packages or namespaces;
- methods with the same name on different types;
- local variables shadowing outer variables;
- language-specific qualification syntax.

find_symbol returns a locator in exactly this shape so it can be passed directly to the other semantic tools.

### 4.3 No opaque public symbol IDs

The API does not introduce a symbol_id.

Reasons:

- source positions are inspectable by a human;
- source positions are stateless;
- they survive MCP reconnects;
- they do not require a server-side identity cache;
- they can be produced by tools other than find_symbol;
- invalid positions fail visibly after source changes instead of resolving to a stale hidden object.

### 4.4 Semantic tools never pretend grep is semantic

If a language/backend does not support a requested operation:

- return an explicit capability error or partial-coverage state;
- do not silently run text search;
- do not return text matches in the semantic result type.

The assistant can explicitly choose start_search as a fallback.

### 4.5 Empty and unsupported are different

Examples:

- find_symbol returns an empty array when the semantic search completed and found no matching symbol.
- find_references returns an empty array when the semantic request completed and found no references.
- find_implementations returns an empty array only when implementation lookup is supported and no implementations were found.
- an unsupported implementation capability returns capability_unsupported, not [].
- a diagnostics request for a supported clean file returns diagnostics: [].
- an unsupported file language is reported as unsupported_language.

### 4.6 Initial Go support does not change the public contract

The Go MVP is an implementation milestone, not a Go-specific API.

No field in the public schemas is named go_module, gopls, go_package, or similar.

---

## 5. Public catalog change

After approval and implementation, the public developer catalog grows from 25 tools to 29 tools.

New Semantic Code group:

1. find_symbol
2. find_references
3. find_implementations
4. get_diagnostics

No existing public tool is removed or renamed by this change.

---

## 6. Shared contracts

### 6.1 Workspace

All four tools require:

    workspace: string

Workspace resolution follows the existing public API contract:

- canonical owner/workspace and owner/workspace.agent are accepted;
- a bare workspace name is accepted only when unambiguous;
- ambiguous bare names fail;
- the assistant should reuse canonical workspace names returned by discovery.

### 6.2 Paths

All public semantic paths are absolute paths inside the selected workspace filesystem.

Rules:

- relative paths are rejected;
- paths are normalized before use;
- returned paths are canonical absolute paths as seen by the workspace Agent;
- read-only symlink traversal is allowed only to locations visible to the workspace Agent;
- semantic operations do not grant filesystem access beyond what the authenticated workspace already exposes;
- find_symbol.root may be a regular file or a directory;
- get_diagnostics.paths must contain regular files;
- scope_path in reference/implementation tools may be a regular file or directory.

### 6.3 Public source coordinates

The public API uses one coordinate system independent of LSP.

Position:

    {
      "line": 123,
      "column": 17
    }

Rules:

- line is 1-based;
- column is 1-based;
- column counts Unicode code points, not UTF-8 bytes, UTF-16 code units, or visual screen cells;
- a tab counts as one code point;
- the Agent converts between public coordinates and the backend's negotiated position encoding;
- invalid line/column positions fail with invalid_position;
- no public caller needs to know the backend position encoding.

### 6.4 Range

    {
      "start": { "line": 123, "column": 17 },
      "end":   { "line": 123, "column": 25 }
    }

Rules:

- start is inclusive;
- end is exclusive;
- both positions use the public 1-based coordinate system;
- a zero-length range has identical start and end positions.

### 6.5 Semantic target / locator

Reusable symbol target:

    {
      "path": "/home/coder/work/repo/server/server.go",
      "line": 123,
      "column": 17
    }

The locator returned by find_symbol must be accepted unchanged as target by find_references and find_implementations.

The locator should point to the start of the symbol's semantic selection/name range whenever the backend provides one.

### 6.6 Normalized symbol kinds

Public symbol kinds use stable lowercase strings.

Initial normalized vocabulary:

- file
- module
- namespace
- package
- class
- method
- property
- field
- constructor
- enum
- interface
- function
- variable
- constant
- string
- number
- boolean
- array
- object
- key
- null
- enum_member
- struct
- event
- operator
- type_parameter
- trait
- macro
- type
- unknown

A backend-specific kind that cannot be mapped safely becomes unknown.

The API must not expose numeric LSP SymbolKind values.

### 6.7 Result limits

The existing public limit rule applies.

For each new field named limit:

- limit is required;
- minimum is 0;
- 0 means the complete logical result available from the semantic backend;
- N > 0 means return at most N records;
- the server must not silently replace 0 with a hidden product limit;
- the server must not silently truncate limit=0;
- normal transport/resource budgets still apply;
- if the requested result cannot safely fit, return an explicit response_too_large error.

No hidden "top 20" or similar default is allowed.

### 6.8 No cursor in v1

The four tools do not introduce a new pagination/session subsystem.

Reason:

- reference and implementation backends normally compute a complete result set before returning;
- workspace-symbol backends do not provide a portable continuation protocol;
- a positive limit is primarily for bounded inspection;
- when truncated=true, the assistant can repeat the same query with a larger limit or 0.

This keeps the first semantic API small and stateless.

A cursor may be added later only if real usage demonstrates that repeated larger-limit queries are materially inefficient.

### 6.9 Context snippets

find_symbol, find_references, find_implementations, and get_diagnostics accept:

    context_lines: integer

Rules:

- optional;
- default: 0;
- minimum: 0;
- maximum: 10;
- 0 means no source snippet;
- N means include at most N source lines before and N source lines after the relevant range;
- context never changes semantic matching;
- context is only convenience payload;
- context is bounded by the same response-safety budget as every other MCP response.

Context shape:

    {
      "start_line": 120,
      "end_line": 126,
      "text": "literal source excerpt"
    }

### 6.10 Coverage

Semantic engines do not always provide a portable proof that every possible result has been indexed.

Every query-style semantic response therefore includes:

    {
      "coverage": {
        "status": "complete | partial | unknown",
        "reason": "optional human-readable explanation"
      }
    }

Definitions:

- complete: the Agent has a reliable basis to claim the requested scope was fully analyzed for this operation;
- partial: the Agent knows some requested scope/capability was not covered;
- unknown: the semantic request succeeded, but the backend does not provide enough information to guarantee completeness.

The tool must not label a result complete merely because the backend returned successfully.

A public positive limit does not itself make coverage partial. It is represented by truncated=true.

### 6.11 Deterministic result ordering

Where the backend does not define a stable order, the Agent normalizes ordering.

find_symbol:

1. stronger name match before weaker match when relevant;
2. symbol name lexicographically;
3. path lexicographically;
4. start line;
5. start column.

find_references:

1. path lexicographically;
2. range start line;
3. range start column;
4. range end line;
5. range end column.

find_implementations:

1. path lexicographically;
2. range start line;
3. range start column.

get_diagnostics:

1. path lexicographically;
2. range start line;
3. range start column;
4. severity: error, warning, information, hint;
5. source;
6. message.

### 6.12 Duplicate records

Exact duplicate semantic locations returned by a backend are deduplicated before public limiting.

The Agent must not merge different semantic locations merely because their snippets or names look identical.

### 6.13 StructuredContent and content

Successful semantic results are primarily structured records and belong in structuredContent.

Short source context requested with context_lines is carried in the relevant structured result record because it must stay associated with that semantic location. This is intentionally bounded and is analogous to the text field in existing search results.

The same record must not be duplicated into a prose/table rendering in content.

Normal successful response:

    content: []
    structuredContent: { ... }

Errors:

- isError: true;
- actionable TextContent;
- structured recovery fields when useful;
- no duplicate serialized error object.

### 6.14 Read-only behavior

All four tools are read-only with respect to user source files.

They may:

- start or reuse an internal language-server process;
- read files;
- read project configuration;
- read module/dependency caches;
- allow the language server/toolchain to perform its normal dependency resolution according to the workspace environment.

They must not:

- apply workspace edits requested by the language server;
- rename files;
- rewrite source;
- accept arbitrary workspace/executeCommand requests from the backend;
- run backend-provided shell commands as a side effect of these tools.

---

## 7. Shared output types

### 7.1 Position

    {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "line":   { "type": "integer", "minimum": 1 },
        "column": { "type": "integer", "minimum": 1 }
      },
      "required": ["line", "column"]
    }

### 7.2 Range

    {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "start": { "$ref": "#/$defs/position" },
        "end":   { "$ref": "#/$defs/position" }
      },
      "required": ["start", "end"]
    }

### 7.3 Target / locator

    {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "path":   { "type": "string", "minLength": 1 },
        "line":   { "type": "integer", "minimum": 1 },
        "column": { "type": "integer", "minimum": 1 }
      },
      "required": ["path", "line", "column"]
    }

### 7.4 Context

    {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "start_line": { "type": "integer", "minimum": 1 },
        "end_line":   { "type": "integer", "minimum": 1 },
        "text":       { "type": "string" }
      },
      "required": ["start_line", "end_line", "text"]
    }

### 7.5 Coverage

    {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "status": {
          "type": "string",
          "enum": ["complete", "partial", "unknown"]
        },
        "reason": { "type": "string" }
      },
      "required": ["status"]
    }

---

## 8. Tool: find_symbol

### 8.1 Purpose

Find semantic symbols by name inside an explicit file or directory scope.

The tool is for discovery.

It does not identify symbols by plain text occurrence.

Typical use:

1. find_symbol with root=/repo and query=Serve;
2. select the desired result by path/kind/container;
3. pass result.locator directly to find_references or find_implementations.

### 8.2 Public description

Find semantic code symbols by name inside a file or directory.

This tool uses the workspace semantic engine rather than text search. root is required and limits the semantic search scope. Results include a reusable locator that can be passed directly to find_references or find_implementations.

limit is required: use 0 for all logical results available from the semantic backend, or a positive value to bound the returned records. context_lines optionally includes bounded source context around each symbol.

### 8.3 Input schema

    {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "workspace": {
          "type": "string",
          "description": "Workspace name using the normal public workspace resolution rules."
        },
        "root": {
          "type": "string",
          "minLength": 1,
          "description": "Required absolute file or directory that scopes semantic symbol search."
        },
        "query": {
          "type": "string",
          "minLength": 1,
          "description": "Symbol name text to match."
        },
        "match": {
          "type": "string",
          "enum": ["exact", "prefix", "substring"],
          "description": "Name matching rule. Defaults to exact."
        },
        "kinds": {
          "type": "array",
          "minItems": 1,
          "uniqueItems": true,
          "items": {
            "type": "string",
            "enum": [
              "file", "module", "namespace", "package", "class", "method",
              "property", "field", "constructor", "enum", "interface",
              "function", "variable", "constant", "string", "number",
              "boolean", "array", "object", "key", "null", "enum_member",
              "struct", "event", "operator", "type_parameter", "trait",
              "macro", "type", "unknown"
            ]
          },
          "description": "Optional normalized symbol-kind filter. Omit to allow all kinds."
        },
        "limit": {
          "type": "integer",
          "minimum": 0,
          "description": "Required result limit. 0 means all logical results available from the backend."
        },
        "context_lines": {
          "type": "integer",
          "minimum": 0,
          "maximum": 10,
          "description": "Optional source context lines before and after each symbol. Defaults to 0."
        }
      },
      "required": ["workspace", "root", "query", "limit"]
    }

### 8.4 Matching semantics

match defaults to exact.

exact:

    result.name == query

prefix:

    result.name begins with query

substring:

    result.name contains query

Public matching is case-sensitive.

The backend may use its own indexing/query mechanism to obtain candidates, but the Agent must apply the public match rule before returning results.

Important portability rule:

- a backend may not guarantee that workspace-wide prefix/substring candidate generation is exhaustive;
- in that case coverage must be unknown or partial;
- file-scoped search can normally obtain the complete document symbol tree and then apply matching locally.

### 8.5 Symbol result shape

    {
      "name": "ServeHTTP",
      "kind": "method",
      "language": "go",
      "path": "/home/coder/work/repo/server/http.go",
      "container": "Server",
      "detail": "func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request)",
      "range": {
        "start": { "line": 91, "column": 1 },
        "end":   { "line": 140, "column": 2 }
      },
      "selection_range": {
        "start": { "line": 91, "column": 18 },
        "end":   { "line": 91, "column": 27 }
      },
      "locator": {
        "path": "/home/coder/work/repo/server/http.go",
        "line": 91,
        "column": 18
      },
      "context": {
        "start_line": 90,
        "end_line": 92,
        "text": "..."
      }
    }

Required fields:

- name
- kind
- language
- path
- selection_range
- locator

Optional fields:

- container
- detail
- range
- context

detail is opportunistic. The implementation must not issue an expensive hover request for every result solely to guarantee detail.

range may be omitted when the backend only supplies the semantic selection/location range.

selection_range must identify the symbol name/token as precisely as the backend allows.

### 8.6 Output schema

    {
      "type": "object",
      "properties": {
        "symbols": {
          "type": "array",
          "items": { "$ref": "#/$defs/semantic_symbol" }
        },
        "returned_count": {
          "type": "integer",
          "minimum": 0
        },
        "observed_count": {
          "type": "integer",
          "minimum": 0,
          "description": "Number of matching candidates observed before applying the public positive limit."
        },
        "truncated": {
          "type": "boolean",
          "description": "True only when the public positive limit omitted observed results."
        },
        "coverage": {
          "$ref": "#/$defs/coverage"
        }
      },
      "required": [
        "symbols",
        "returned_count",
        "observed_count",
        "truncated",
        "coverage"
      ]
    }

### 8.7 Empty result

No matching symbol is a normal success:

    {
      "symbols": [],
      "returned_count": 0,
      "observed_count": 0,
      "truncated": false,
      "coverage": {
        "status": "complete"
      }
    }

The coverage value still matters. An empty result with coverage=unknown means only that the backend returned no candidate; it is not a proof that the workspace contains no such symbol.

### 8.8 Example request

    {
      "workspace": "developer/coder",
      "root": "/home/coder/work/coder",
      "query": "StartProcess",
      "match": "exact",
      "kinds": ["function", "method"],
      "limit": 20,
      "context_lines": 1
    }

---

## 9. Tool: find_references

### 9.1 Purpose

Find semantic references to the symbol at an exact source position.

The tool does not accept symbol_name.

The normal target is the locator returned by find_symbol.

### 9.2 Public description

Find semantic references to the symbol at an exact source position.

target uses an absolute file path plus 1-based line and Unicode-code-point column. Pass find_symbol.locator directly when available.

This tool never falls back to text search. include_declaration defaults to false. scope_path optionally filters semantic references to one file or directory after semantic resolution.

limit is required: use 0 for all logical references returned by the semantic backend, or a positive value to bound the returned records.

### 9.3 Input schema

    {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "workspace": {
          "type": "string"
        },
        "target": {
          "$ref": "#/$defs/target"
        },
        "include_declaration": {
          "type": "boolean",
          "description": "Include the symbol declaration/definition when the backend supports that distinction. Defaults to false."
        },
        "scope_path": {
          "type": "string",
          "minLength": 1,
          "description": "Optional absolute file or directory filter applied to semantic results."
        },
        "limit": {
          "type": "integer",
          "minimum": 0,
          "description": "Required result limit. 0 means all logical references returned by the backend."
        },
        "context_lines": {
          "type": "integer",
          "minimum": 0,
          "maximum": 10,
          "description": "Optional source context lines before and after each reference. Defaults to 0."
        }
      },
      "required": ["workspace", "target", "limit"]
    }

### 9.4 scope_path semantics

scope_path never changes symbol resolution.

The semantic backend first resolves the target and computes semantic references. The Agent then filters those semantic locations by scope_path.

This is intentionally different from performing a text search inside scope_path.

If scope_path is a file, only references in that file remain.

If scope_path is a directory, only references whose canonical paths are descendants of that directory remain.

### 9.5 Reference result shape

    {
      "path": "/home/coder/work/repo/server/run.go",
      "language": "go",
      "range": {
        "start": { "line": 211, "column": 9 },
        "end":   { "line": 211, "column": 21 }
      },
      "locator": {
        "path": "/home/coder/work/repo/server/run.go",
        "line": 211,
        "column": 9
      },
      "containing_symbol": {
        "name": "runServer",
        "kind": "function"
      },
      "context": {
        "start_line": 210,
        "end_line": 212,
        "text": "..."
      }
    }

Required:

- path
- language
- range
- locator

Optional:

- containing_symbol
- context

containing_symbol is enrichment only. Failure to determine it must not fail the reference query.

The API intentionally does not invent read/write/call/import roles unless a future backend-neutral implementation can guarantee them.

### 9.6 Output schema

    {
      "type": "object",
      "properties": {
        "target": {
          "$ref": "#/$defs/target"
        },
        "references": {
          "type": "array",
          "items": { "$ref": "#/$defs/reference" }
        },
        "returned_count": {
          "type": "integer",
          "minimum": 0
        },
        "observed_count": {
          "type": "integer",
          "minimum": 0
        },
        "truncated": {
          "type": "boolean"
        },
        "coverage": {
          "$ref": "#/$defs/coverage"
        }
      },
      "required": [
        "target",
        "references",
        "returned_count",
        "observed_count",
        "truncated",
        "coverage"
      ]
    }

The returned target is the normalized canonical target used by the Agent.

### 9.7 Empty result

If the backend supports semantic references and returns none:

    {
      "references": [],
      "returned_count": 0,
      "observed_count": 0,
      "truncated": false,
      "coverage": {
        "status": "complete"
      }
    }

A backend/capability failure must not be converted into this shape.

### 9.8 Example request

    {
      "workspace": "developer/coder",
      "target": {
        "path": "/home/coder/work/coder/coderd/mcp/mcp.go",
        "line": 224,
        "column": 48
      },
      "include_declaration": false,
      "scope_path": "/home/coder/work/coder/coderd",
      "limit": 100,
      "context_lines": 1
    }

---

## 10. Tool: find_implementations

### 10.1 Purpose

Find semantic implementations of the symbol at an exact source position.

Typical targets:

- interface type;
- interface method;
- abstract method;
- trait/protocol member;
- other backend-defined implementation-capable symbol.

The public API does not assume a particular object model.

### 10.2 Public description

Find semantic implementations of the symbol at an exact source position.

target uses an absolute file path plus 1-based line and Unicode-code-point column. Pass find_symbol.locator directly when available.

This tool never falls back to text search or type-name matching. If the selected semantic backend does not support implementation lookup, the tool returns capability_unsupported rather than an empty implementation list.

scope_path optionally filters semantic implementation locations to one file or directory.

### 10.3 Input schema

    {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "workspace": {
          "type": "string"
        },
        "target": {
          "$ref": "#/$defs/target"
        },
        "scope_path": {
          "type": "string",
          "minLength": 1,
          "description": "Optional absolute file or directory filter applied after semantic implementation lookup."
        },
        "limit": {
          "type": "integer",
          "minimum": 0,
          "description": "Required result limit. 0 means all logical implementations returned by the backend."
        },
        "context_lines": {
          "type": "integer",
          "minimum": 0,
          "maximum": 10,
          "description": "Optional source context lines before and after each implementation. Defaults to 0."
        }
      },
      "required": ["workspace", "target", "limit"]
    }

### 10.4 Implementation result shape

    {
      "path": "/home/coder/work/repo/backend/postgres/store.go",
      "language": "go",
      "range": {
        "start": { "line": 44, "column": 1 },
        "end":   { "line": 102, "column": 2 }
      },
      "selection_range": {
        "start": { "line": 44, "column": 22 },
        "end":   { "line": 44, "column": 35 }
      },
      "locator": {
        "path": "/home/coder/work/repo/backend/postgres/store.go",
        "line": 44,
        "column": 22
      },
      "name": "PostgresStore",
      "kind": "struct",
      "container": "postgres",
      "detail": "type PostgresStore struct",
      "context": {
        "start_line": 43,
        "end_line": 45,
        "text": "..."
      }
    }

Required:

- path
- language
- range
- locator

Optional enrichment:

- selection_range
- name
- kind
- container
- detail
- context

The semantic backend may return only a location. Enrichment is best-effort and must not change whether the implementation itself is returned.

### 10.5 Output schema

    {
      "type": "object",
      "properties": {
        "target": {
          "$ref": "#/$defs/target"
        },
        "implementations": {
          "type": "array",
          "items": { "$ref": "#/$defs/implementation" }
        },
        "returned_count": {
          "type": "integer",
          "minimum": 0
        },
        "observed_count": {
          "type": "integer",
          "minimum": 0
        },
        "truncated": {
          "type": "boolean"
        },
        "coverage": {
          "$ref": "#/$defs/coverage"
        }
      },
      "required": [
        "target",
        "implementations",
        "returned_count",
        "observed_count",
        "truncated",
        "coverage"
      ]
    }

### 10.6 Empty vs unsupported

Supported request with no implementations:

    success + implementations: []

Unsupported semantic capability:

    MCP error code capability_unsupported

The implementation must never convert one into the other.

### 10.7 Example request

    {
      "workspace": "developer/coder",
      "target": {
        "path": "/home/coder/work/coder/codersdk/workspaceagents.go",
        "line": 85,
        "column": 6
      },
      "limit": 100,
      "context_lines": 2
    }

---

## 11. Tool: get_diagnostics

### 11.1 Purpose

Return semantic/compiler-style diagnostics for one or more explicit files.

The primary coding-agent use case is:

1. edit several files;
2. pass exactly those files to get_diagnostics;
3. fix semantic/type errors;
4. then run broader build/tests as needed.

The tool is intentionally file-oriented.

It does not claim to replace a full project build/test command.

### 11.2 Public description

Get semantic diagnostics for one or more explicit workspace files.

The Agent synchronizes current file contents with the semantic backend and returns diagnostics associated with the requested files. A clean supported file returns no diagnostics.

paths is explicit and file-oriented; use normal build/test commands through start_process for authoritative whole-project validation.

limit is required and applies to the total returned diagnostic records across all requested files.

### 11.3 Input schema

    {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "workspace": {
          "type": "string"
        },
        "paths": {
          "type": "array",
          "minItems": 1,
          "maxItems": 100,
          "uniqueItems": true,
          "items": {
            "type": "string",
            "minLength": 1
          },
          "description": "Absolute regular-file paths to analyze."
        },
        "minimum_severity": {
          "type": "string",
          "enum": ["error", "warning", "information", "hint"],
          "description": "Lowest severity to return. Defaults to hint. warning returns errors and warnings; information returns errors, warnings, and information."
        },
        "include_related_information": {
          "type": "boolean",
          "description": "Include backend-provided related diagnostic locations/messages. Defaults to false."
        },
        "limit": {
          "type": "integer",
          "minimum": 0,
          "description": "Required total diagnostic result limit. 0 means all logical diagnostics returned for the requested files."
        },
        "context_lines": {
          "type": "integer",
          "minimum": 0,
          "maximum": 10,
          "description": "Optional source context lines before and after each diagnostic. Defaults to 0."
        }
      },
      "required": ["workspace", "paths", "limit"]
    }

### 11.4 Severity normalization

Public severities:

1. error
2. warning
3. information
4. hint

minimum_severity includes the named severity and every more severe level.

Examples:

- error -> only errors;
- warning -> errors + warnings;
- information -> errors + warnings + information;
- hint -> all four severities.

Unknown backend severities normalize to information unless the backend semantics justify a safer mapping.

### 11.5 Per-file status

Because paths may contain multiple languages, one unsupported file must not necessarily fail the whole request.

Each requested file receives a file result:

    {
      "path": "/home/coder/work/repo/server/server.go",
      "language": "go",
      "status": "ok",
      "freshness": "fresh",
      "diagnostic_count": 2
    }

status enum:

- ok
- unsupported_language
- capability_unsupported
- not_ready
- error

freshness is present only for status=ok.

freshness enum:

- fresh
- unknown

The tool must never knowingly return stale diagnostics as status=ok.

fresh means the Agent can associate the diagnostic snapshot with the current synchronized file contents.

unknown means the diagnostic snapshot was obtained after current-content synchronization, but the backend protocol did not provide enough version information to prove exact freshness.

If only a known-stale snapshot exists, return not_ready or error for that file rather than stale diagnostics.

### 11.6 Diagnostic shape

    {
      "path": "/home/coder/work/repo/server/server.go",
      "severity": "error",
      "message": "cannot use x (variable of type string) as int value in argument to f",
      "range": {
        "start": { "line": 131, "column": 14 },
        "end":   { "line": 131, "column": 15 }
      },
      "source": "compiler",
      "code": "IncompatibleAssign",
      "code_href": "https://example.invalid/optional",
      "tags": ["unnecessary"],
      "related_information": [
        {
          "path": "/home/coder/work/repo/server/types.go",
          "range": {
            "start": { "line": 21, "column": 1 },
            "end":   { "line": 21, "column": 10 }
          },
          "message": "parameter declared here"
        }
      ],
      "context": {
        "start_line": 130,
        "end_line": 132,
        "text": "..."
      }
    }

Required:

- path
- severity
- message
- range

Optional:

- source
- code
- code_href
- tags
- related_information
- context

Normalized diagnostic tags:

- unnecessary
- deprecated

Unknown backend tags are omitted rather than exposed as backend numeric values.

### 11.7 Output schema

    {
      "type": "object",
      "properties": {
        "files": {
          "type": "array",
          "items": { "$ref": "#/$defs/diagnostic_file_status" }
        },
        "diagnostics": {
          "type": "array",
          "items": { "$ref": "#/$defs/diagnostic" }
        },
        "returned_count": {
          "type": "integer",
          "minimum": 0
        },
        "observed_count": {
          "type": "integer",
          "minimum": 0,
          "description": "Number of diagnostics observed after severity filtering but before the public positive limit."
        },
        "truncated": {
          "type": "boolean"
        },
        "coverage": {
          "$ref": "#/$defs/coverage"
        }
      },
      "required": [
        "files",
        "diagnostics",
        "returned_count",
        "observed_count",
        "truncated",
        "coverage"
      ]
    }

### 11.8 Coverage for multi-file diagnostics

complete:

- every requested file was status=ok;
- the backend considers the requested diagnostic capability available;
- no requested file was skipped.

partial:

- at least one requested file succeeded and at least one was unsupported/not_ready/error.

unknown:

- all requested files returned usable results, but backend completeness/freshness guarantees are insufficient to claim complete.

If no requested file can be semantically analyzed, the tool should return an error when a single common failure explains the request, or structured per-file failures when the paths fail for different reasons.

### 11.9 Example request

    {
      "workspace": "developer/coder",
      "paths": [
        "/home/coder/work/coder/coderd/mcp/mcp.go",
        "/home/coder/work/coder/coderd/mcp/mcp_test.go"
      ],
      "minimum_severity": "warning",
      "include_related_information": true,
      "limit": 100,
      "context_lines": 1
    }

---

## 12. Error model

Semantic tools use normal MCP errors with actionable text plus a stable semantic error code in structured recovery metadata when useful.

Required semantic error codes:

### invalid_path

A required semantic path is malformed, relative, missing, or has an invalid type.

### invalid_position

line/column does not identify a valid source position in the target file.

### unsupported_language

No semantic backend is installed/configured for the target language.

The error should include the detected language when known.

Initial Go MVP example:

    No semantic backend is available for Python in this workspace. The current semantic implementation supports Go only.

### semantic_backend_unavailable

A backend should exist for the language but its executable/runtime is unavailable.

For the Go MVP, a missing gopls binary is this error, not unsupported_language.

### semantic_backend_start_failed

The backend executable exists but failed during process start or initialization.

Include bounded stderr/debug detail when safe.

### semantic_backend_not_ready

The backend did not become ready before the MCP request deadline.

This error is retryable.

### capability_unsupported

The selected backend is available but does not support the requested semantic capability.

Required example:

    find_implementations must return capability_unsupported when the backend lacks implementation support.

### semantic_request_failed

The backend was initialized but the semantic request itself failed.

### response_too_large

The complete logical result requested with limit=0 cannot fit within the existing MCP response/resource budget.

The error must recommend a positive limit and/or lower context_lines.

### context_unavailable

Not a whole-tool error.

If semantic results are valid but source context cannot be read, omit context from that record. Do not discard the semantic result.

---

## 13. MCP annotations

Proposed annotations for all four tools:

    readOnlyHint: true
    destructiveHint: false
    idempotentHint: true

openWorldHint requires care.

The public operation is local semantic analysis, but a language server may invoke normal language-toolchain dependency resolution under the workspace's existing environment.

For the Go MVP, set:

    openWorldHint: true

until testing proves gopls is configured so semantic requests cannot cause external dependency resolution.

Do not incorrectly mark the tools closed-world merely because their public result is read-only.

---

## 14. Semantic Manager architecture

### 14.1 Location

Semantic orchestration belongs in the Coder Workspace Agent.

It does not belong in:

- the ChatGPT gateway;
- the MCP proxy;
- the Coder control-plane process as a language server host;
- a separate semantic sidecar for the first implementation.

The language server must run next to the workspace filesystem and toolchain.

### 14.2 Lifecycle ownership

Backend lifecycle belongs to the workspace Agent, not to an MCP session.

Desired topology:

    Chat/MCP session A ----\
    Chat/MCP session B ----- Coder Workspace Agent -> Semantic Manager -> gopls
    Chat/MCP session C ----/

Multiple MCP sessions using the same workspace/project should reuse the same backend instance.

Closing a ChatGPT connection must not kill the backend.

### 14.3 Lazy startup

No semantic backend is started merely because the workspace Agent starts.

First semantic request for a project/language:

1. detect language;
2. resolve semantic project root;
3. check backend availability;
4. start backend if necessary;
5. initialize backend;
6. synchronize required documents;
7. execute semantic request;
8. keep backend available for reuse.

This avoids starting unused language servers and avoids unnecessary startup CPU/RAM.

### 14.4 Backend key

Internal backend instances should be keyed at least by:

    workspace Agent
    language
    semantic project root
    backend implementation

For the first Go implementation:

    language = go
    backend = gopls

### 14.5 Idle lifecycle

The public API does not promise a permanent backend process.

The Agent may evict an idle backend to control resources.

Requirements:

- eviction must be transparent to later semantic calls;
- later calls lazily restart it;
- an MCP session disconnect must not itself trigger eviction;
- no public symbol identity depends on backend process lifetime.

The first implementation should keep the policy simple and configurable rather than embedding it into the public API.

### 14.6 Backend crash

If a read-only semantic backend dies during a request:

- mark the backend instance unhealthy;
- capture bounded stderr for telemetry/error reporting;
- restart on the next request;
- a single transparent retry of the same read-only semantic request is allowed only when it is safe to establish that no public mutation occurred.

Do not create an infinite restart/retry loop.

### 14.7 Concurrency

Initialization/startup for the same backend key must be single-flight.

Two simultaneous first semantic requests must not start two gopls processes for the same project root.

After initialization, independent read-only semantic requests may execute concurrently when supported by the backend client implementation.

Document synchronization notifications for the same file must preserve order.

### 14.8 Internal process visibility

The semantic backend is an Agent-managed internal service.

It should:

- be visible in the ordinary OS process table when list_processes sees it;
- not appear as a user-started durable process in list_sessions;
- not receive a public process_id;
- be shut down gracefully with the workspace Agent when possible.

---

## 15. Go MVP backend

### 15.1 Current image capability

The currently deployed Developer Workspace image already advertises:

    go:golang.org/x/tools/gopls v0.21.0

Therefore the first Go implementation does not require a Developer Workspace OCI rebuild.

### 15.2 LSP mapping

The Go adapter should use normal gopls/LSP capabilities.

find_symbol:

- file root -> textDocument/documentSymbol where supported;
- directory root -> workspace/symbol plus canonical path filtering;
- public name matching is applied by the Agent after candidate retrieval.

find_references:

- textDocument/references;
- includeDeclaration maps from include_declaration.

find_implementations:

- textDocument/implementation;
- capability availability must be checked from initialize result.

get_diagnostics:

- prefer pull diagnostics when advertised and reliable;
- otherwise track publishDiagnostics notifications after synchronizing requested documents;
- never knowingly return a pre-synchronization stale diagnostic snapshot as current.

### 15.3 Go project-root resolution

For a target file, the initial Go adapter should choose a root using this order:

1. an applicable enclosing go.work root;
2. otherwise the nearest enclosing go.mod directory;
3. otherwise a safe ad-hoc root accepted by gopls.

The root algorithm must be covered by tests for:

- a normal single-module repository;
- nested modules;
- go.work;
- a Go file outside a module.

find_symbol with a directory root may intersect more than one Go module. The implementation must either:

- query the correct set of semantic roots and merge results; or
- return partial coverage with a reason.

It must not silently search only the first arbitrary module and claim complete coverage.

---

## 16. Document synchronization

Semantic correctness must not assume that all source changes were made through one MCP session.

### 16.1 Files changed by Agent filesystem tools

When write_file, edit_file, edit_multiple_files, or move_file changes a document known to an active semantic backend, the Agent should notify/synchronize the semantic manager.

The semantic manager should not wait for an unrelated future request if it already knows a file changed.

### 16.2 Files changed externally

Before a semantic operation directly involving a file, the semantic manager must compare its tracked document state with the current workspace file.

If the current file changed outside the semantic manager:

- update/open the current contents before the semantic request;
- advance the internal document version;
- do not intentionally query semantic state for an older in-memory version.

### 16.3 Position-encoding translation

The LSP backend may negotiate UTF-16, UTF-8, or another supported position encoding.

The semantic manager must translate every public Unicode-code-point position to/from the negotiated encoding.

The translation layer must be tested with:

- ASCII;
- tabs;
- non-ASCII Latin/Cyrillic letters;
- supplementary Unicode characters such as emoji.

---

## 17. Readiness and indexing

A semantic request must distinguish:

- backend process started;
- LSP initialize completed;
- document synchronized;
- requested semantic operation completed.

"Process exists" is not sufficient evidence of semantic readiness.

The first implementation must not invent a fake fixed sleep such as "wait two seconds after starting gopls".

Use protocol/request completion as the primary readiness signal.

If the MCP request deadline expires while initialization/indexing is still blocking the requested operation:

- return semantic_backend_not_ready or the appropriate timeout-wrapped semantic error;
- mark it retryable;
- leave a healthy backend process available for the next attempt when safe.

---

## 18. Capability discovery

At initialization, the backend adapter records the capabilities advertised by the semantic engine.

Public behavior must be capability-driven.

Examples:

- references supported -> find_references is available for that backend;
- implementationProvider absent -> find_implementations returns capability_unsupported;
- diagnostic capability absent and no supported push-diagnostic path -> diagnostics capability_unsupported.

Do not hardcode "all gopls versions support everything" as the public correctness rule.

---

## 19. Security and trust boundaries

### 19.1 Backend executable trust

For the Go MVP, gopls comes from the version-pinned Developer Workspace image.

The semantic manager must not:

- download a different gopls executable on demand;
- run a repository-provided binary named gopls before the image-pinned binary;
- install npm/pip/cargo packages as a side effect of semantic tool invocation.

### 19.2 Server-initiated requests

The semantic client must implement only the client-side LSP requests/notifications needed for safe operation.

It must reject or safely handle server requests that would mutate the workspace.

In particular, semantic read tools must not automatically apply workspace edits.

### 19.3 Backend output

Backend stderr/logs may contain source paths, package names, or diagnostics.

Rules:

- do not expose unbounded backend logs to the assistant;
- bound diagnostic error detail;
- preserve the existing secret-redaction expectations where command/environment metadata is surfaced;
- never place environment secrets in semantic structuredContent.

---

## 20. Telemetry and observability

Semantic tooling needs enough telemetry to debug language-server failures without storing source content unnecessarily.

Record safe metadata such as:

- tool name;
- workspace ID;
- language;
- backend kind/version;
- semantic project root hash or safe path according to existing trace policy;
- backend reused vs started;
- initialization duration;
- request duration;
- result count;
- coverage status;
- error code;
- restart count;
- timeout/cancel.

Do not store:

- full source snippets;
- full diagnostic messages when existing privacy rules prohibit content storage;
- document contents;
- environment variable values;
- tokens/secrets.

Existing MCP trace retention rules remain applicable.

---

## 21. Interaction with existing public tools

### 21.1 start_search remains text search

start_search is not deprecated.

Use start_search when the assistant wants:

- literal/regex text;
- comments;
- strings;
- filenames;
- config files;
- unsupported-language fallback.

Use semantic tools when the assistant wants:

- symbol identity;
- code references;
- implementations;
- semantic diagnostics.

### 21.2 start_process remains authoritative for build/test

get_diagnostics does not replace:

    go test
    go vet
    golangci-lint
    tsc
    pytest
    cargo test
    compiler/build commands

A language server may intentionally report a different subset of problems from the project's authoritative build pipeline.

### 21.3 File edits remain separate

Semantic tools are read-only.

An assistant performs changes with the existing exact-edit/write tools.

This separation keeps semantic navigation from silently mutating source.

---

## 22. Initial acceptance tests

Implementation is not complete until the following tests pass.

### 22.1 Schema tests

For all four tools:

- workspace required;
- every limit required and minimum 0;
- context_lines defaults to 0 and rejects >10;
- all path fields enforce absolute-path semantics;
- unknown input properties rejected;
- outputSchema advertises the implemented structuredContent shape;
- annotations match the approved spec.

### 22.2 Coordinate tests

- public positions are 1-based;
- range end is exclusive;
- ASCII location round-trip;
- Cyrillic/non-ASCII identifier location round-trip;
- emoji before target symbol does not shift public column incorrectly;
- tab before target symbol counts as one public column unit.

### 22.3 find_symbol tests

Go fixture contains:

- package function;
- method;
- interface;
- struct;
- field;
- constant;
- duplicate method names on different receiver types.

Verify:

- exact match;
- prefix match;
- substring match;
- kind filtering;
- file root;
- directory root;
- limit=1 sets truncated when more observed;
- limit=0 returns all safe logical results;
- locator can be passed unchanged to find_references;
- deterministic ordering;
- no plain-text comment/string false positive.

### 22.4 find_references tests

Verify:

- reference to package function;
- method references with same-named methods on other receiver types do not mix;
- local variable shadowing does not mix symbols;
- include_declaration false/true;
- scope_path file;
- scope_path directory;
- limit semantics;
- duplicate locations deduplicated;
- no comment/string text matches;
- unsupported language returns unsupported_language.

### 22.5 find_implementations tests

Verify:

- interface implementation;
- interface method implementation where supported;
- multiple implementations;
- no implementations -> successful empty array;
- unsupported capability -> capability_unsupported;
- scope_path;
- limit semantics;
- best-effort symbol enrichment failure does not remove a valid implementation location.

### 22.6 get_diagnostics tests

Verify:

- clean Go file -> [];
- syntax/type error -> normalized diagnostic;
- severity filtering;
- multiple files;
- one supported + one unsupported file -> partial coverage and per-file statuses;
- related information on/off;
- limit semantics across files;
- diagnostics correspond to current file after edit;
- known stale snapshot is never returned as fresh;
- file changed externally between semantic calls is resynchronized.

### 22.7 Lifecycle tests

- first Go semantic request starts one gopls;
- second request reuses it;
- two simultaneous first requests still create one backend instance;
- two different MCP sessions reuse the same workspace semantic backend;
- MCP session close does not kill gopls;
- backend crash is detected;
- later request restarts backend;
- Agent shutdown attempts graceful backend shutdown;
- backend never appears as a user durable list_sessions process.

### 22.8 Project-root tests

- normal go.mod project;
- nested go.mod;
- go.work;
- directory scope covering multiple modules;
- file outside module.

### 22.9 Safety tests

- semantic calls never modify tracked source files;
- backend workspace/applyEdit is not applied;
- no on-demand executable/package installation;
- response budget rejects unsafe limit=0 + large-context result rather than truncating silently;
- backend stderr is bounded;
- secrets are not surfaced through semantic metadata.

### 22.10 Regression tests

Existing 25 public tools must retain:

- names;
- schemas;
- aliases;
- limit semantics;
- process/session behavior;
- gateway duplicate-process behavior.

The semantic implementation must not weaken current tool metadata consistency tests.

---

## 23. Live acceptance plan for Go MVP

After local/unit/e2e tests and before broader language support:

1. deploy the Coder build containing the four tools through the normal reviewed release process;
2. do not rebuild the Developer Workspace image;
3. use the existing gopls in a current Developer Workspace;
4. verify all four tools against the biptec/coder Go repository;
5. compare find_references results against representative rg/text-search queries to confirm semantic filtering;
6. test duplicate names/receiver methods;
7. edit a Go file with edit_file and verify get_diagnostics reflects the new current contents;
8. revert the temporary edit;
9. confirm repeated semantic calls reuse the same backend process;
10. collect assistant usability feedback before designing additional language support.

Only after the Go tools are proven useful should the Developer Workspace OCI image be expanded with additional language servers.

---

## 24. Future language expansion

Future OCI image work may add version-pinned semantic backends such as:

- TypeScript/JavaScript language server;
- Python language server;
- rust-analyzer;
- clangd;
- Java language server;
- other project-required engines.

Adding one must require:

- pinned executable/version in developer-workspace;
- capability metadata;
- backend adapter/configuration;
- language/root detection tests;
- the same semantic conformance tests;
- no public schema fork by language.

The four public tool names and core shapes must remain unchanged.

If a future language has semantics that cannot be represented correctly by this contract, stop and review the API rather than adding hidden language-specific fields.

---

## 25. Implementation boundaries

Approval of this document would authorize implementation of the Go MVP only.

It would not automatically authorize:

- Developer Workspace OCI changes;
- adding additional languages;
- production deployment;
- schema expansion beyond this document;
- rename_symbol;
- code actions;
- automatic text-search fallback;
- a separate semantic service/sidecar.

Any material contract change discovered during implementation must be brought back for review before silently changing the public API.

Small internal implementation choices that preserve this contract do not require schema reapproval.

---

## 26. Proposed implementation order after approval

1. Add internal semantic types and coordinate conversion.
2. Add Semantic Manager lifecycle to Workspace Agent.
3. Add gopls backend initialization and capability discovery.
4. Implement file/document synchronization.
5. Implement find_symbol.
6. Implement find_references.
7. Implement find_implementations.
8. Implement diagnostics collection.
9. Add public MCP schemas and descriptions.
10. Add consistency/safety tests.
11. Run focused tests.
12. Run full relevant Go tests/lint.
13. Perform manual Go acceptance in a Developer Workspace.
14. Reconcile implementation line-by-line against this specification.
15. Prepare PR for review.
16. Production release/deploy remains a separate explicit gate.

---

## 27. Review questions before implementation

The following decisions are intentionally explicit so they can be approved or changed before coding:

1. find_symbol.root is required rather than searching an entire Coder workspace implicitly.
2. public source coordinates are 1-based Unicode-code-point positions.
3. find_references/find_implementations identify a symbol only by path+line+column.
4. no public symbol_id is introduced.
5. no semantic tool silently falls back to start_search.
6. match supports exact/prefix/substring, default exact.
7. limits are required and 0 means all logical backend results.
8. v1 has no semantic cursor/pagination session.
9. context_lines is optional, default 0, max 10.
10. get_diagnostics is file-oriented and accepts up to 100 explicit files.
11. diagnostics never knowingly return a stale snapshot as current.
12. the initial backend is existing gopls; no OCI rebuild for the Go MVP.
13. language-server lifecycle belongs to Workspace Agent and is shared across MCP sessions.
14. backend startup is lazy.
15. unsupported and empty results are distinct.
16. implementation lookup must return capability_unsupported when the backend does not support it.
17. production deployment remains separately approved.

These contract decisions were accepted by the human reviewer on 2026-09-23. Any material deviation discovered during implementation must return to review before changing the public contract.
