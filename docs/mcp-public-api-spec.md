# Coder Remote MCP Public API Specification

**Status:** approved implementation specification / durable handoff; implementation reconciled and locally verified on 2026-09-21; ready for PR review
**Project:** `biptec/coder`
**Target branch family:** `custom/v2.35.3`
**Specification date:** 2026-09-21
**Primary implementation worktree:** `/home/coder/work/coder-mcp-api-v2`
**Current implementation branch:** `feat/mcp-api-v2`
**Historical stable baseline where this redesign restarted:** `v2.35.3.26`
**Current branch base:** `v2.35.3.32` / `a2a2d4f6d4f46e50e5c54c905cf425d774f7c20a`, which includes PR #70, PR #71, and PR #72. The feature branch was rebased onto this head on 2026-09-21 after verifying that PR #72 only changed command-activity persistence/database code and did not alter this public API contract. Re-check `custom/v2.35.3` immediately before the final PR.

---

## 1. Why this document exists

This is the authoritative handoff for the assistant-facing Coder Remote MCP redesign.

It must be sufficient for a new assistant with no conversation history to recover:

- the target public tool catalog;
- the exact intent of every tool;
- the intended input schema;
- the model-facing output format;
- process/search lifecycle semantics;
- SSH semantics;
- pagination and timeout semantics;
- MCP safety annotations;
- error and recovery behavior;
- the real Desktop Commander behavior that was used as evidence;
- what Coder should copy, and where Coder must intentionally do better;
- compatibility and migration constraints;
- the required test/acceptance matrix before a PR is allowed.

If code and this specification disagree during the current redesign, do **not** silently preserve the code. Determine whether the code is an unfinished implementation artifact or whether the spec needs an explicitly approved amendment.

---

## 2. Non-negotiable project constraints

1. **Do not deploy production automatically.** Building, testing, publishing, preloading, and planning are allowed. Production `make deploy`/Helm rollout on `coder1` always requires explicit human approval.
2. **Do not create a PR merely because code compiles.** Public tools require unit + integration + MCP E2E acceptance.
3. **Do not repeat the v2.35.3.27 failure mode.** That release introduced unapproved architecture changes and runtime regressions. The redesign started again from the stable `.26` baseline.
4. **Do not invent extra public tools to solve internal implementation problems.** The public catalog is intentionally compact.
5. **Model UX matters as much as Go types.** Tests must verify what the assistant actually sees after `tools/call`.
6. **If an infrastructure/test dependency is unavailable, keep working on independent areas instead of stopping.** Record the blocker and continue.

---

## 3. Research basis

### 3.1 Primary real-world reference: Desktop Commander Remote MCP

The current Desktop Commander product was tested directly through the real Remote Desktop Commander connector, not inferred from its historical public GitHub repository.

Observed device:

- Desktop Commander version: **0.2.51**
- Linux
- device name: `coder-copy-hel1`

Actual tools were invoked against temporary data and processes.

Important observed behavior:

- Most tools return MCP `content[]` containing **plain TextContent**, not a JSON object serialized as text.
- `read_file` returns a small pagination header followed by literal file contents.
- `read_multiple_files` returns separate readable blocks and tolerates per-file failures.
- `list_directory` returns line-oriented `[FILE]` / `[DIR]` entries.
- `get_file_info` returns `key: value` lines.
- `write_file`, `create_directory`, `move_file`, terminate operations return short success text.
- `edit_block` success returns the edited content; mismatch returns actionable fuzzy-match diagnostics.
- `start_search` returns a search handle **and initial results in the same call**.
- `start_process` returns a PID **and initial output in the same call**.
- `read_process_output` returns literal process output plus completion status.
- `interact_with_process` writes input **and returns newly observed output in the same call**.
- `list_sessions` is compact text.
- `list_processes` returns the entire OS process table as text; on the real machine the response was about 60 KB. This is useful evidence about context cost, but Coder must not solve it with a hidden default limit: the assistant chooses `limit` when it wants a smaller response.
- `get_config` is a notable exception: it uses structured data as well. This proves Desktop Commander itself does not use one universal output shape.
- Desktop Commander tool history showed the actual MCP payloads as `content: [{type:"text", text:"..."}]` for normal tools.

### 3.2 What to copy from Desktop Commander

Copy these ideas:

- model-first readable output;
- initial results from durable starter operations;
- typed structured results for record-oriented operations;
- raw file/process payloads without an inner JSON serialization layer;
- actionable edit mismatch diagnostics;
- compact, predictable field sets;
- follow-up handles only when more output/results are needed.

### 3.3 What Coder should do better

Do **not** copy these weaknesses:

- mixing service-generated headers and opaque file/process payload in the same `TextContent`; Coder uses `structuredContent` to make that boundary explicit;
- making a large `list_processes` response impossible to control; Coder should expose an optional assistant-selected `limit` without imposing one by default;
- `start_process` failing to report the final exit state when the process already completed during the initial wait;
- output that requires a second call merely to discover completion if completion is already known;
- ambiguous process timeout/lifetime semantics;
- tool schemas that require unnecessary parameters only because the implementation does.

---

## 4. Core output philosophy

### 4.1 Internal representation stays typed

Internally, Go handlers should continue returning typed structures.

```text
typed Go result
    -> MCP presentation renderer
    -> CallToolResult { content, structuredContent }
```

Do not convert backend logic to ad-hoc string-producing functions.

### 4.2 Use the protocol boundary without duplicating model context

The assistant-facing response should choose the smallest representation that preserves meaning. A fact should normally appear **once** in the model-visible tool result. Incidental overlap that is intrinsic to a natural payload (for example, a file path in a unified-diff header, or process output that happens to resemble metadata) is not considered intentional duplication and must not be rewritten merely to remove it.

- `structuredContent` is the canonical location for structured records, server metadata, handles, status, cursors, counts, paths, and other machine-meaningful fields;
- `content[]` is reserved for data whose natural representation is text or another MCP content block, especially opaque file/process payload, diffs, and actionable error text;
- do not add a human-readable `TextContent` rendering of a structured result merely for convenience when `structuredContent` already conveys the same information.

The current `mcp-go` wire type requires the `content` field. For structured-only success results, return `"content": []`; do not fill it with a duplicate table, summary, or JSON serialization.

Do **not** serialize the complete typed result as JSON inside a text block:

```json
{
  "content": [
    {
      "type": "text",
      "text": "{\"output\":\"hello\\nworld\\n\",\"process_id\":\"...\",\"running\":false,\"exit_code\":0}"
    }
  ]
}
```

For a process result, prefer one MCP response like:

```json
{
  "content": [
    {
      "type": "text",
      "text": "hello\nworld\n"
    }
  ],
  "structuredContent": {
    "process_id": "3dc8699f-...",
    "status": "completed",
    "exit_code": 0,
    "output_cursor": 12
  }
}
```

This is intentionally different from Desktop Commander 0.2.51, which commonly prefixes raw output with service-generated text such as `Initial output:` in the same `TextContent`. Coder should keep that good model-first readability where useful, but use MCP structure to eliminate ambiguity when the payload itself can contain arbitrary text.

### 4.3 Opaque payload rule

For tools that return opaque user/process data, **never mix Coder-generated metadata into the same text payload**.

This applies at minimum to:

- `read_file`;
- each successful file payload in `read_multiple_files`;
- process stdout/stderr returned by `start_process`;
- process stdout/stderr returned by `execute_shell_command`;
- `read_process_output`;
- post-input stdout/stderr returned by `interact_with_process`.

For these responses:

- `content[n].text` is the payload itself, with no `File:`, `Process:`, `Status:`, cursor, line-number prefix, or other Coder header injected into it;
- `structuredContent` carries all server metadata needed to interpret or continue the operation;
- an empty payload may use an empty `content` array; do not invent text such as `No new output.` and make it look like process output;
- file/process payload remains literal text and is not JSON-escaped by an inner serialization layer.

A process may legitimately print text such as `Status: completed` or the contents of another file. Keeping metadata outside the payload means the model never has to infer whether those bytes came from Coder or from the process.

### 4.4 `type`, encoding, and MIME are different concepts

`{"type":"text"}` is the MCP content-block type. It does **not** describe the original file's encoding or MIME type.

For a normal text `read_file`, do not redundantly add `encoding: "text"` to `structuredContent` merely because the MCP block is text.

For binary file reads, the bytes are represented as base64 inside a `TextContent` block, so metadata must say how to interpret that string:

```json
{
  "content": [
    {
      "type": "text",
      "text": "iVBORw0KGgoAAAANSUhEUgAA..."
    }
  ],
  "structuredContent": {
    "path": "/home/coder/image.png",
    "content_encoding": "base64",
    "mime_type": "image/png",
    "start_byte": 0,
    "end_byte": 4095,
    "size": 18432,
    "next_offset": 4096,
    "eof": false
  }
}
```

`mime_type` is optional and should be included only when known reliably. `content_encoding: "base64"` is required for a base64 payload. Do not emit `content_encoding: "text"` for ordinary text.

### 4.5 Structured/list results

When the result is naturally a record, list, table, state object, or search result set, return it **once** in `structuredContent` and use an empty `content` array.

Examples include:

- workspaces, apps, capabilities;
- recent tool calls;
- file metadata and directory entries;
- search lifecycle state and search matches;
- tracked sessions and OS processes.

Do not also render the same rows as a text table. The model can consume the typed structure directly, and duplicating it spends context tokens without adding information.

### 4.6 Action results

For successful actions without a natural payload, return only canonical fields in `structuredContent` and use `content: []`.

Examples: `write_file`, `create_directory`, `move_file`, `signal_process`, `stop_search`.

For edit tools, a unified diff is a real model-facing payload and belongs in `content[]`; path/replacement counts belong in `structuredContent`. Do not prepend a prose summary that repeats those structured fields.

Errors are different: actionable error text belongs in `content[]` with `isError: true`, because the explanation itself is the useful model-facing payload.

### 4.7 Output should enable the next action

A good response should make the next action obvious without requiring the model to parse prose for identifiers:

- handles (`process_id`, `search_id`) are structured fields;
- continuation state (`next_cursor`, `next_offset`) is structured;
- status and completion are structured;
- text is returned only when it carries information that is not already represented by the structured fields.

Error output should be actionable, not merely descriptive.

### 4.8 No fake values

Never emit a fake/placeholder process exit code while a process is still running.

When `status` is `running`, omit `exit_code` from `structuredContent`. Add it only when the process is known to have exited.

### 4.9 Publish `outputSchema` for structured results

Every public tool that returns `structuredContent` should declare an MCP `outputSchema` describing that canonical structure. The runtime `structuredContent` must validate against it.

This is important for the same reason the input schema is important: the assistant should not have to infer whether a handle is called `process_id`, whether a cursor is numeric or opaque, or whether an exit code can exist while a process is running.

`content[]` does not mirror the output schema. It complements `structuredContent` only when there is distinct text/media payload to return.

### 4.10 Host compatibility

Target-host note, verified on 2026-09-21 against the current OpenAI ChatGPT/Plugins MCP documentation: both `structuredContent` and `content` are provided to the model, while `_meta` is hidden from the model. That makes `structuredContent` suitable for canonical handles/state in this ChatGPT-facing API.

The target public contract therefore relies on MCP `structuredContent`; do not weaken it by injecting metadata into opaque file/process payloads as a fallback, and do not duplicate structured records into `TextContent`. The current `mcp-go` source comments recommend equivalent unstructured content for backwards compatibility, but that is a `SHOULD`, not a requirement of this ChatGPT-facing contract; for structured-only success results we intentionally send an empty `content` array. If a different/legacy host needs duplication for compatibility, handle that at a host-specific connector/presentation layer and test it explicitly rather than charging every ChatGPT call the duplicate context cost.

---

## 5. Target public developer catalog

The target assistant-facing catalog contains **25 tools**.

### Workspace / discovery

- `list_workspaces`
- `get_workspace`
- `list_apps`
- `get_workspace_capabilities`
- `list_recent_tool_calls`

### Filesystem

- `read_file`
- `read_multiple_files`
- `write_file`
- `edit_file`
- `edit_multiple_files`
- `get_file_info`
- `list_directory`
- `create_directory`
- `move_file`

### Search

- `start_search`
- `get_search_results`
- `list_searches`
- `stop_search`

### Execution / process

- `start_process`
- `execute_shell_command`
- `read_process_output`
- `interact_with_process`
- `signal_process`
- `list_sessions`
- `list_processes`

### Explicitly removed from the target public catalog

- **`exec`** is removed.

Rationale: once `start_process` returns initial output, `exec` and `start_process` are two policy variants over the same durable backend. That is not a useful semantic distinction for the model.

The useful distinction is:

- `start_process(argv=[...])`: structured direct execution, no shell parsing;
- `execute_shell_command(command="...")`: intentional shell syntax.

Internal SDK names may remain unchanged if changing them is unnecessary.

---

## 6. Shared data contracts

### 6.1 Workspace reference

Workspace-scoped tools use:

```text
workspace: string
```

Accepted forms remain compatible with the current resolver:

- `workspace`
- `owner/workspace`
- `workspace.agent`
- `owner/workspace.agent`

When owner omission is ambiguous, require the caller to use the owner-qualified form.

All workspace-scoped **public** tools, including `get_workspace`, use the same `workspace: string` input. Internal SDK/backend compatibility may still map this value into an existing ID/name field, but that implementation detail must not leak into the assistant-facing schema.

### 6.2 SSH execution descriptor

SSH is an **execution transport**, not a filesystem mode.

Execution tools accept:

```json
{
  "ssh": {
    "host": "user@example.com",
    "identity_file": "/home/coder/.ssh/id_ed25519",
    "port": 2222
  }
}
```

Schema:

- `host: string`: required when `ssh` is present; may be `host`, `user@host`, or an OpenSSH config alias.
- `identity_file?: string`: absolute path inside the workspace; never return its value in session metadata.
- `port?: integer`: 1..65535.

Safety:

- values are passed as structured argv/options to OpenSSH;
- never interpolate `host`, `identity_file`, or `port` into shell syntax;
- reject leading-option injection, whitespace/NUL host abuse, invalid port, and relative identity paths;
- remote commands execute through explicit POSIX `sh -c` when shell execution is requested, not through an unknown login-shell contract;
- do not reintroduce the v2.35.3.27 zsh `status` variable bug.

### 6.3 Timeout ceiling

Deployment setting:

```text
CODER_MCP_TOOL_TIMEOUT_MAX
default: 280s
```

This is the wall-clock ceiling for **one MCP call**, including workspace readiness, agent connection, operation/observation, and response preparation.

It is **not** process lifetime.

If an explicit `wait_timeout_ms` is larger than the deployment ceiling, reject it with a validation error. Do not silently clamp.

Do not silently subtract a fixed implementation reserve from an accepted wait. The actual observation can be shorter only because earlier phases of the same MCP call already consumed wall-clock budget or because the parent request ends.

Durable processes/searches survive the end of the observation call.

### 6.4 Cursor rules

Use `cursor` only for continuation, but choose its type according to the data model:

- Process output cursor: integer absolute byte cursor.
- Search-result cursor: integer result index within the durable search session.
- Directory cursor: opaque string tied to alphabetical continuation; do not expose a fragile array index when the directory may change.
- Activity cursor: opaque string; it must continue toward older records without duplication when newer activity arrives.
- Tracked-session cursor: opaque string based on newest-first start ordering plus a stable tie-breaker.
- OS-process cursor: opaque string based on newest-first process start ordering plus a stable tie-breaker such as PID.

A cursor is returned only when continuation is actually available. Do not manufacture `next_cursor` when an unlimited request returned the complete logical result.

### 6.5 Assistant-controlled limits and deterministic ordering

The public assistant-facing API must **not impose arbitrary default result limits or arbitrary public maximum limits**. If a tool exposes `limit` (or an equivalent such as `max_results`), that parameter is optional: the assistant supplies it only when it wants to restrict the amount returned. When the parameter is omitted, the tool returns the complete logical result currently available for that operation.

Do not silently substitute a default `limit`. Do not invent a public `max` merely to protect context size. The assistant is responsible for choosing a limit when a smaller response is useful. A true transport, storage, protocol, or security boundary is a separate implementation constraint and must be justified explicitly rather than presented as a product default.

Pagination/cursors are still supported when the assistant explicitly chooses a limit. When no explicit limit truncates the result, there is no artificial pagination boundary to manufacture.

Ordering must be deterministic and useful without requiring the model to request a sort mode:

- process/session lists: newest-started first;
- activity/history lists: newest first;
- filesystem listings: alphabetical ascending by relative path/name;
- numeric sequences: natural numeric order where the number is the semantic ordering key;
- search file results: alphabetical by path; content matches: path first, then line number numerically;
- for a tool whose natural ordering is unclear, inspect the real Desktop Remote Commander behavior before defining it.

Do **not** add a public `sort` parameter merely for flexibility. Add one only if a real assistant use case demonstrates that the fixed natural ordering is insufficient.

### 6.6 Input consistency rules

The public input surface is considered frozen around these consistency rules:

- every workspace-scoped tool uses `workspace: string`;
- `wait_timeout_ms` has the same meaning everywhere it exists: omitted/0 means do not intentionally wait, a positive value is the maximum requested observation wait, and a value above `CODER_MCP_TOOL_TIMEOUT_MAX` is rejected rather than clamped;
- result `limit`/`max_results` parameters are optional and assistant-controlled, with no hidden default result limit and no arbitrary public maximum;
- `cursor` is optional continuation state and follows section 6.4;
- `read_file`/`read_multiple_files` keep the simple `binary?: boolean` input; do not replace it with a more verbose mode enum without a demonstrated need;
- booleans with a natural false state (`binary`, `interactive`, `regex`, `case_sensitive`, `include_hidden`, `replace_all`, `overwrite`) default to false when omitted unless a tool section explicitly states otherwise;
- do not add a general `sort` input while deterministic natural ordering is sufficient.

---

## 7. Tool specifications

### 7.1 list_workspaces

#### Purpose

Discover workspaces visible to the authenticated user.

#### Inputs

None.

#### Output

Return workspace records once in `structuredContent`; use `content: []`.

```json
{
  "content": [],
  "structuredContent": {
    "workspaces": [
      {"workspace":"developer/coder","status":"running"},
      {"workspace":"developer/agent-memory","status":"running"},
      {"workspace":"developer/test","status":"stopped"}
    ]
  }
}
```

Include only fields useful to workspace selection; do not mirror the full raw Coder API object.

#### Safety annotations

- readOnly: true
- destructive: false
- idempotent: true
- openWorld: false

#### Errors

Authentication/authorization errors should state the failure directly. Do not expose tokens or internal credentials.

---

### 7.2 get_workspace

#### Purpose

Return a richer summary for one workspace.

#### Inputs

```text
workspace: string
```

Use the shared workspace reference contract from section 6.1. The backend may resolve name or ID internally, but the public schema is consistently named `workspace`.

#### Output

Return the useful workspace/agent fields once in `structuredContent`; use `content: []`. Do not return the full raw Coder API object.

```json
{
  "content": [],
  "structuredContent": {
    "workspace": "developer/coder",
    "status": "running",
    "build_number": 42,
    "template": "developer-workspace",
    "agents": [
      {
        "name": "main",
        "status": "connected",
        "version": "v2.35.3.32",
        "last_connected_at": "2026-09-21T10:00:00Z"
      }
    ]
  }
}
```

Only expose fields useful to an assistant.

#### Safety annotations

read-only / closed-world.

---

### 7.3 list_apps

#### Inputs

- `workspace: string`

#### Output

Return app records once in `structuredContent`; use `content: []`.

```json
{
  "content": [],
  "structuredContent": {
    "apps": [
      {"name":"code-server","url":"http://127.0.0.1:13337","status":"healthy"},
      {"name":"web","url":"https://example...","status":"healthy"}
    ]
  }
}
```

Do not serialize or render the apps array again inside `TextContent`.

#### Safety annotations

read-only / closed-world.

---

### 7.4 get_workspace_capabilities

#### Inputs

- `workspace: string`

#### Purpose

Inspect preinstalled developer capabilities before attempting installations.

#### Output

Return the capability groups once in `structuredContent`; use `content: []`.

```json
{
  "content": [],
  "structuredContent": {
    "groups": [
      {"name":"Languages","items":["Go 1.xx","Node 24.x","Python 3.12"]},
      {"name":"Infrastructure","items":["kubectl","OpenTofu","Packer"]},
      {"name":"Browsers","items":["Chromium / Playwright"]}
    ]
  }
}
```

Preserve enough version/path detail for implementation decisions, but avoid dumping irrelevant manifest internals.

#### Safety annotations

read-only / closed-world.

---

### 7.5 list_recent_tool_calls

#### Inputs

- `workspace: string`: required.
- `limit?: integer`: optional assistant-selected result limit; no default and no arbitrary public maximum.
- `cursor?: string`: opaque continuation cursor, used when continuing an explicitly limited page.

No global unscoped activity aggregation in the developer surface.

#### Ordering

Newest first.

A continuation cursor must continue toward older records without duplication if newer calls are inserted between pages.

#### Output

Return activity records and continuation metadata once in `structuredContent`; use `content: []`.

```json
{
  "content": [],
  "structuredContent": {
    "calls": [
      {"started_at":"2026-09-21T10:31:12Z","status":"done","tool":"start_process","process_id":"abc..."},
      {"started_at":"2026-09-21T10:30:54Z","status":"running","tool":"execute_shell_command","process_id":"def..."},
      {"started_at":"2026-09-21T10:30:21Z","status":"done","tool":"read_file"}
    ],
    "next_cursor": "eyJ2..."
  }
}
```

Omit `next_cursor` when no continuation exists.

Never include:

- command output;
- file contents;
- environment values;
- stdin;
- tokens;
- secrets.

#### Safety annotations

read-only / closed-world.

---

## 8. Filesystem tools

### 8.1 read_file

#### Inputs

- `workspace: string`
- `path: string`: absolute.
- `binary?: boolean`: default false.
- `offset?: integer`
  - text: 1-based line offset, default 1;
  - binary: 0-based byte offset, default 0.
- `limit?: integer`: optional assistant-selected amount to read; no default and no arbitrary public maximum. In text mode it is a line count; in binary mode it is a byte count. If omitted, read the complete remaining file from `offset`.

#### Text output

`content[0].text` is **only the literal file text**. Do not inject path headers or line numbers into the payload. All location/pagination metadata belongs in `structuredContent`.

```json
{
  "content": [
    {
      "type": "text",
      "text": "package main\n\nimport \"fmt\"\n"
    }
  ],
  "structuredContent": {
    "path": "/home/coder/main.go",
    "start_line": 1,
    "end_line": 3,
    "total_lines": 20,
    "next_offset": 4,
    "eof": false
  }
}
```

If the complete remaining file was returned, omit `next_offset` and set `eof: true`. For an empty text file, return an empty text payload (or an empty `content` array if the MCP implementation permits it consistently) and structured metadata showing `total_lines: 0`, `eof: true`.

Do not add `content_encoding: "text"`; the MCP block type already says the block contains text.

#### Binary output

Binary bytes are base64-encoded because the payload travels in `TextContent`. Mark that transformation explicitly:

```json
{
  "content": [
    {
      "type": "text",
      "text": "AAECAwQF..."
    }
  ],
  "structuredContent": {
    "path": "/home/coder/image.bin",
    "content_encoding": "base64",
    "start_byte": 0,
    "end_byte": 1023,
    "size": 8192,
    "next_offset": 1024,
    "eof": false
  }
}
```

Include `mime_type` only when it is known reliably.

#### Safety

read-only / closed-world. Never accept URLs.

---

### 8.2 read_multiple_files

#### Inputs

- `workspace: string`
- `files: array`: non-empty. Do not impose an arbitrary public maximum number of files; the assistant chooses the batch size.
- Per item:
  - `path: string`
  - `binary?: boolean`
  - `offset?: integer`
  - `limit?: integer`

#### Output

Each successfully read file gets its own raw `TextContent` block. `structuredContent.files[]` maps each file to its content block by `content_index`; failed items carry structured error metadata and do not create fake file payload.

```json
{
  "content": [
    {
      "type": "text",
      "text": "alpha\nbeta\n"
    }
  ],
  "structuredContent": {
    "files": [
      {
        "path": "/home/coder/a.txt",
        "content_index": 0,
        "start_line": 1,
        "end_line": 2,
        "total_lines": 2,
        "eof": true
      },
      {
        "path": "/home/coder/missing.txt",
        "error": {
          "code": "not_found",
          "message": "File not found"
        }
      }
    ]
  }
}
```

Binary items use `content_encoding: "base64"` in that file's metadata. One file failure must not fail the entire batch.

#### Safety

read-only / closed-world.

---

### 8.3 write_file

#### Inputs

- `workspace: string`
- `path: string`: absolute.
- `content: string`
- `encoding?: "text" | "base64"`, default text.

The operation replaces the complete file; it does not append.

#### Output

Return write metadata once in `structuredContent`; use `content: []`.

```json
{
  "content": [],
  "structuredContent": {
    "path": "/home/coder/config.json",
    "bytes_written": 842
  }
}
```

Do not add a prose success message that repeats `path` or `bytes_written`.

#### Safety annotations

- readOnly: false
- destructive: true
- idempotent: true
- openWorld: false

Preserve existing file permissions on overwrite.

---

### 8.4 edit_file

#### Inputs

- `workspace: string`
- `path: string`
- `edits: array`
  - `search: string`
  - `replace: string`
  - `replace_all?: boolean`, default false
  - `expected_replacements?: integer`, minimum 1

**Do not add new public parameters such as `dry_run` unless explicitly approved.** A partial implementation currently contains `dry_run`; that is not frozen by this spec and must not silently become public merely because code exists.

#### Apply behavior

- exact replacement;
- when `replace_all=false`, search must resolve unambiguously according to the existing contract;
- preserve file permissions;
- compute a unified diff for success output.

#### Success output

The unified diff is the model-facing text payload; canonical edit metadata remains structured and is not repeated in prose.

```json
{
  "content": [
    {
      "type": "text",
      "text": "--- /home/coder/main.go\n+++ /home/coder/main.go\n@@ ...\n-old\n+new\n"
    }
  ],
  "structuredContent": {
    "path": "/home/coder/main.go",
    "replacements": 1
  }
}
```

#### Exact-match failure output

Make the error actionable.

If a close candidate can be safely computed, return:

```text
Edit not applied: exact match not found.

Closest match: 92%

beta needle{-s-}{+\n+}

Use the exact text above and retry.
```

This is inspired by real Desktop Commander `edit_block` behavior.

Do not silently fuzzy-apply an edit.

#### Safety annotations

destructive / non-idempotent / closed-world.

---

### 8.5 edit_multiple_files

#### Inputs

- `workspace: string`
- `files: array`
  - `path: string`
  - `edits: array` using the same edit object as `edit_file`.

#### Validation behavior

Phase 1 validates/computes all edits before writing. If any edit fails validation, write nothing.

A later filesystem write failure can still leave earlier files committed; do not claim cross-file transactionality unless real filesystem transactions are implemented.

#### Output

Return only the unified diffs in `content[]` and canonical per-file counts in `structuredContent`; do not add a summary that repeats the structured counts.

```json
{
  "content": [
    {
      "type": "text",
      "text": "--- /home/coder/a.go ---\n<unified diff>\n\n--- /home/coder/b.go ---\n<unified diff>\n"
    }
  ],
  "structuredContent": {
    "files": [
      {"path":"/home/coder/a.go","replacements":1},
      {"path":"/home/coder/b.go","replacements":2}
    ],
    "files_edited": 2
  }
}
```

On validation failure, identify every relevant failure when safe rather than returning an opaque generic error.

#### Safety

destructive / non-idempotent / closed-world.

---

### 8.6 get_file_info

#### Inputs

- `workspace: string`
- `path: string`

The path may be a file or directory.

#### Output

Return file/directory metadata once in `structuredContent`; use `content: []`.

```json
{
  "content": [],
  "structuredContent": {
    "path": "/home/coder/main.go",
    "type": "file",
    "size": 1248,
    "mode": "-rw-r--r--",
    "modified_at": "2026-09-21T10:00:00Z"
  }
}
```

Prefer `get_file_info` as the public name even though it also accepts directories, because it matches mature filesystem MCP precedent and is already agreed.

#### Safety

read-only / closed-world.

---

### 8.7 list_directory

#### Inputs

- `workspace: string`
- `path: string`
- `depth?: integer`: default 1. This selects recursion depth; do not invent an arbitrary public maximum solely to constrain response size.
- `include_hidden?: boolean`: default false
- `cursor?: string`: opaque alphabetical continuation cursor when an explicit limit is used
- `limit?: integer`: optional assistant-selected entry limit; no default and no arbitrary public maximum

#### Output

Return directory entries once in `structuredContent`; use `content: []`. Entries are sorted alphabetically by relative path/name.

```json
{
  "content": [],
  "structuredContent": {
    "entries": [
      {"path":"go.mod","type":"file","size":812,"mode":"-rw-r--r--"},
      {"path":"internal/","type":"directory"},
      {"path":"README.md","type":"file","size":4198,"mode":"-rw-r--r--"},
      {"path":"src/","type":"directory"}
    ],
    "next_cursor": "eyJwYXRoIjoic3JjLyJ9"
  }
}
```

Omit `next_cursor` when the request returned the complete result.

#### Safety

read-only / closed-world.

---

### 8.8 create_directory

#### Inputs

- `workspace: string`
- `path: string`
- `parents?: boolean`

Existing directory is success.

#### Output

```json
{
  "content": [],
  "structuredContent": {
    "path": "/home/coder/project/tmp",
    "created": true
  }
}
```

If it already exists, return success with `created: false`; keep `content: []`.

#### Safety annotations

- readOnly: false
- destructive: false
- idempotent: true
- openWorld: false

---

### 8.9 move_file

Despite the public name, the implementation supports files and directories.

#### Inputs

- `workspace: string`
- `source: string`
- `dest: string`
- `overwrite?: boolean`, default false.

#### Output

```json
{
  "content": [],
  "structuredContent": {
    "source": "/home/coder/a.txt",
    "dest": "/home/coder/archive/a.txt"
  }
}
```

#### Safety annotations

destructive / non-idempotent / closed-world.

---

## 9. Search lifecycle

Searches are ephemeral Agent search sessions, not long-term persisted jobs. Result-count limiting is assistant-controlled; session lifetime/retention is a separate implementation concern. A running search has no hidden fixed execution timeout: it runs until it completes, is explicitly stopped with `stop_search`, or the Agent itself terminates.

The key UX principle is:

> `start_search` returns both the handle and any initial results already available.

A second tool call should be needed only for continuation.

---

### 9.1 start_search

#### Inputs

- `workspace: string`
- `root: string`: absolute.
- `query: string`
- `mode: "files" | "content"`
- `regex?: boolean`
- `case_sensitive?: boolean`
- `include_hidden?: boolean`
- `max_results?: integer`: optional assistant-selected cap on retained search results; no default and no arbitrary public maximum. If omitted, do not impose an artificial result cap.
- `wait_timeout_ms?: integer`
  - default **0**;
  - 0 means return as soon as the search session is created, together with whatever non-blocking initial snapshot is already available;
  - an explicit positive value asks the tool to observe for that duration;
  - cannot exceed `CODER_MCP_TOOL_TIMEOUT_MAX`.

#### Output

Return search lifecycle metadata and result records once in `structuredContent`; use `content: []`.

Content-search example:

```json
{
  "content": [],
  "structuredContent": {
    "search_id": "search_abc123",
    "status": "running",
    "results_available": 2,
    "results": [
      {"path":"/home/coder/src/main.go","line":42,"text":"needle"},
      {"path":"/home/coder/src/server.go","line":11,"text":"needle"}
    ],
    "next_cursor": 2
  }
}
```

For file search, each result record needs only its path. If complete and fully returned, set `status: "completed"` and omit `next_cursor`.

Initial output follows the same assistant-controlled limit policy. Do not silently truncate it with an implementation default. File-search results are ordered alphabetically by path; content-search results are ordered by path and then line number numerically.

#### Safety annotations

Read-only regarding workspace/external data. Ephemeral search-session bookkeeping does not make it destructive.

---

### 9.2 get_search_results

#### Inputs

- `workspace: string`
- `search_id: string`
- `cursor?: integer`: default 0
- `limit?: integer`: optional assistant-selected result limit; no default and no arbitrary public maximum. If omitted, return all results currently available from the cursor.
- `wait_timeout_ms?: integer`
  - default 0 / omitted = immediate snapshot;
  - explicit positive value waits for new results/completion up to the deployment ceiling.

#### Output

Use the same response shape as `start_search`: lifecycle/result records once in `structuredContent`, with `content: []`.

```json
{
  "content": [],
  "structuredContent": {
    "search_id": "search_abc123",
    "status": "running",
    "results_available": 57,
    "results": [
      {"path":"/path/a.go","line":12,"text":"..."},
      {"path":"/path/b.go","line":98,"text":"..."}
    ],
    "next_cursor": 40
  }
}
```

If complete and no more results, omit `next_cursor`.

#### Safety

read-only / closed-world.

---

### 9.3 list_searches

#### Inputs

- `workspace: string`

#### Output

Return search-session records once in `structuredContent`; use `content: []`.

```json
{
  "content": [],
  "structuredContent": {
    "searches": [
      {"search_id":"search_abc123","status":"running","mode":"content","query":"needle","results":57},
      {"search_id":"search_def456","status":"completed","mode":"files","query":"*.go","results":12}
    ]
  }
}
```

#### Safety

read-only / closed-world.

---

### 9.4 stop_search

#### Inputs

- `workspace: string`
- `search_id: string`

#### Output

```json
{
  "content": [],
  "structuredContent": {
    "search_id": "search_abc123",
    "stopped": true
  }
}
```

Calling stop on an already completed/stopped search should be idempotent where practical; structured state should make that outcome explicit.

No filesystem data is modified.

---

## 10. Execution and process lifecycle

### 10.1 Why there are only two launch tools

There are two semantically distinct launch modes:

#### start_process

Direct structured argv execution.

```json
{"argv":["go","test","./..."]}
```

No shell parsing.

#### execute_shell_command

Intentional shell language.

```json
{"command":"go test ./... | tee /tmp/test.log"}
```

Uses explicit POSIX `sh -c`.

#### Removed: exec

Do not preserve a separate public `exec` only because it previously waited longer.

Observation duration is a parameter/policy of the same durable process lifecycle, not a reason for a separate tool.

---

### 10.2 Common process response contract

Launch/follow-up tools use one canonical structured lifecycle shape while keeping stdout/stderr as opaque `TextContent`.

#### Running with output

```json
{
  "content": [
    {
      "type": "text",
      "text": "Server starting...\nListening on :3000\n"
    }
  ],
  "structuredContent": {
    "process_id": "47d12a...",
    "status": "running",
    "output_cursor": 1842
  }
}
```

#### Completed with output

```json
{
  "content": [
    {
      "type": "text",
      "text": "PASS\nok  example/project  0.43s\n"
    }
  ],
  "structuredContent": {
    "process_id": "47d12a...",
    "status": "completed",
    "exit_code": 0,
    "output_cursor": 1842
  }
}
```

#### No new output

```json
{
  "content": [],
  "structuredContent": {
    "process_id": "47d12a...",
    "status": "running",
    "output_cursor": 1842
  }
}
```

Do not emit fake text such as `No new output.` inside the opaque process payload. Do not emit a fake exit code for running processes. Always expose `process_id`, including when a process completes immediately.

---

### 10.3 start_process

#### Purpose

Start a durable, tracked process through direct argv execution.

#### Inputs

- `workspace: string`
- `argv: string[]`: required, min 1, non-empty executable.
- `workdir?: string`
- `env?: object<string,string>`
- `stdin?: string`: optional initial stdin. Do not freeze an arbitrary public size cap here; any true transport/security request-size ceiling must be justified separately.
- `interactive?: boolean`: default false.
- `ssh?: WorkspaceSSHOptions`
- `wait_timeout_ms?: integer`
  - default **0**;
  - 0 = return immediately after start acknowledgement and a non-blocking initial output snapshot;
  - an explicit positive value asks the tool to wait for initial output/completion for that duration;
  - maximum deployment MCP timeout.

#### Removed inputs

- `command`: shell commands belong to `execute_shell_command`.
- `background`: tracked processes are durable regardless; the flag is not useful to the model.

#### Behavior

1. Ensure workspace/agent readiness.
2. Start process exactly once.
3. Obtain `process_id`.
4. Observe initial output/completion for up to `wait_timeout_ms`.
5. Return current state and initial output.
6. Process lifetime is independent of the MCP call.

#### Output

Use the common process response contract in section 10.2. `content[]` contains only the initial stdout/stderr observed during the requested wait; `structuredContent` always contains `process_id`, current `status`, and `output_cursor`, plus `exit_code` only after completion.

#### Uncertain acknowledgement

If the MCP call disconnects after submission but before acknowledgement, **do not tell the model to blindly rerun the command**.

Error/recovery guidance must say:

```text
Process start acknowledgement was not confirmed.
Use list_sessions for this workspace before retrying; the process may already exist.
```

#### Safety annotations

- readOnly: false
- destructive: true
- idempotent: false
- openWorld: true

OpenWorld is true because the same public tool may execute through optional SSH and MCP annotations are static per tool.

---

### 10.4 execute_shell_command

#### Purpose

Run an intentional POSIX shell command as a durable tracked process.

#### Inputs

- `workspace: string`
- `command: string`: required.
- `workdir?: string`
- `env?: object<string,string>`
- `stdin?: string`: optional initial stdin. Do not freeze an arbitrary public size cap here; any true transport/security request-size ceiling must be justified separately.
- `interactive?: boolean`
- `ssh?: WorkspaceSSHOptions`
- `wait_timeout_ms?: integer`
  - same semantics as `start_process`.

#### Execution contract

Use explicit `sh -c`; do not rely on login-shell-specific behavior.

#### Output

Exactly the same lifecycle response contract as `start_process`: raw observed stdout/stderr only in `content[]`, lifecycle metadata in `structuredContent`.

#### Safety annotations

destructive / non-idempotent / open-world.

---

### 10.5 read_process_output

#### Inputs

- `workspace: string`
- `process_id: string`
- `cursor?: integer`: canonical public default 0 / incremental mode.
- `limit?: integer`: optional assistant-selected output byte limit; no default and no arbitrary public maximum. If omitted, return all currently retained output from the cursor.
- `wait_timeout_ms?: integer`
  - default 0 / omitted = immediate;
  - explicit positive value waits for new output or process completion;
  - cannot exceed deployment ceiling.

#### Public semantics

The assistant-facing API should use incremental cursor semantics. Legacy internal head+tail snapshot behavior may remain for internal/compatibility code but should not be the documented canonical MCP path.

#### Output

Return literal retained stdout/stderr only in `content[]`. Put process state and cursor metadata in `structuredContent`.

```json
{
  "content": [
    {
      "type": "text",
      "text": "<literal stdout/stderr>"
    }
  ],
  "structuredContent": {
    "process_id": "47d12a...",
    "status": "completed",
    "exit_code": 0,
    "output_cursor": 2048
  }
}
```

If old bytes were evicted before the requested cursor, include `output_gap_bytes` in `structuredContent`; do not prepend an eviction warning to the raw process payload.

#### Safety annotations

read-only / closed-world.

The output buffer is already local tracked state even when the original process was remote via SSH.

---

### 10.6 interact_with_process

#### Purpose

Write to stdin of a tracked interactive process **and immediately observe the response**.

#### Inputs

- `workspace: string`
- `process_id: string`
- `data?: string`: optional stdin data. Do not freeze an arbitrary public size cap here; any true transport/security request-size ceiling must be justified separately.
- `close?: boolean`: send EOF after optional data.
- `wait_timeout_ms?: integer`
  - default **0**;
  - 0 = write input and return an immediate non-blocking post-input snapshot;
  - an explicit positive value waits for new output/process transition for that duration;
  - bounded by deployment ceiling.
- `limit?: integer`: optional assistant-selected output byte limit; no default and no arbitrary public maximum. If omitted, return all newly available retained output.

At least `data` or `close=true` must be supplied.

#### Behavior

1. Snapshot the current output cursor.
2. Write data/EOF.
3. If input was accepted, wait for new output/process transition up to the observation timeout.
4. Return only output observed after the pre-write cursor, plus the new cursor/state.

An input acknowledgement must not be lost merely because the post-input observation times out.

#### Output

Return only newly observed stdout/stderr as opaque `TextContent`; input acknowledgement and process state are structured.

```json
{
  "content": [
    {
      "type": "text",
      "text": "interactive-ok\n"
    }
  ],
  "structuredContent": {
    "process_id": "47d12a...",
    "input_accepted": true,
    "status": "running",
    "output_cursor": 2120
  }
}
```

If observation expires without new output, return `content: []` with `input_accepted: true` and the current lifecycle metadata. Do not insert timeout prose into the process payload.

#### Safety annotations

destructive / non-idempotent / open-world, because the tracked session may represent remote execution.

---

### 10.7 signal_process

#### Inputs

- `workspace: string`
- `process_id: string`
- `signal: "interrupt" | "terminate" | "kill"`

Semantics:

- interrupt -> SIGINT / Ctrl-C style;
- terminate -> SIGTERM;
- kill -> SIGKILL.

Target the process group when supported so pipelines/children receive the signal consistently.

Only tracked process/session IDs are accepted. Do **not** allow arbitrary OS PIDs from `list_processes`.

#### Output

Return signal state once in `structuredContent`; use `content: []`.

```json
{
  "content": [],
  "structuredContent": {
    "process_id": "47d12a...",
    "signal": "interrupt",
    "sent": true
  }
}
```

If the process has already exited, treat that idempotently where practical: return `sent: false` and `already_completed: true` in `structuredContent` rather than manufacturing an error or duplicate prose.

#### Safety annotations

destructive / non-idempotent / open-world.

---

### 10.8 list_sessions

#### Purpose

List durable processes/sessions created through the Coder MCP execution layer.

This is **not** an OS process table.

Sessions include processes started by:

- `start_process`;
- `execute_shell_command`;
- migration-era tracked execution calls if still recoverable.

#### Inputs

- `workspace: string`
- `cursor?: string`: opaque newest-first continuation cursor when an explicit limit is used.
- `limit?: integer`: optional assistant-selected session limit; no default and no arbitrary public maximum. If omitted, return all tracked sessions.

#### Output

Return tracked-session records once in `structuredContent`; use `content: []`. Sessions are ordered by process start time descending; ties use a stable deterministic secondary key.

```json
{
  "content": [],
  "structuredContent": {
    "sessions": [
      {"process_id":"47d12a...","status":"running","runtime_ms":42000,"command":"npm run dev","started_at":"2026-09-21T10:31:12Z"},
      {"process_id":"91af...","status":"completed","runtime_ms":400,"command":"go test ./...","started_at":"2026-09-21T10:30:54Z","exit_code":0}
    ],
    "next_cursor": "eyJzdGFydGVkX2F0IjoiLi4uIn0"
  }
}
```

For SSH-backed sessions, include a safe `target` field such as `admin@example.com:2222` in the corresponding structured session record. Never show `identity_file`. Omit `next_cursor` when no continuation exists.

#### Recovery role

After timeout/502/reconnect/uncertain acknowledgement, this is the primary recovery tool before executing a potentially duplicate command.

#### Safety

read-only / closed-world.

---

### 10.9 list_processes

#### Purpose

List actual operating-system processes visible inside the workspace, like `ps`.

This is separate from `list_sessions`.

#### Inputs

- `workspace: string`
- `limit?: integer`: optional assistant-selected process limit; no default and no arbitrary public maximum. If omitted, return all matching OS processes.
- `cursor?: string`: opaque continuation cursor when an explicit limit is used.
- `filter?: string`: case-insensitive match against username/command.

#### Output

Return OS-process records once in `structuredContent`; use `content: []`. Records are ordered by process start time descending so the most recently started processes appear first.

```json
{
  "content": [],
  "structuredContent": {
    "processes": [
      {"pid":456,"ppid":123,"user":"coder","cpu_percent":0.0,"memory_percent":0.1,"elapsed_seconds":40,"command":"sleep 600","started_at":"2026-09-21T10:31:12Z"},
      {"pid":123,"ppid":1,"user":"coder","cpu_percent":0.2,"memory_percent":1.4,"elapsed_seconds":41,"command":"node server.js","started_at":"2026-09-21T10:31:11Z"}
    ],
    "next_cursor": "eyJzdGFydGVkX2F0IjoiLi4uIiwicGlkIjoxMjN9"
  }
}
```

Real Desktop Commander returned roughly 60 KB for a full OS process table during testing. Coder must not silently impose a default limit to avoid this; the assistant supplies `limit` when it wants to control context size. Omit `next_cursor` when the complete result was returned.

#### Mutation boundary

OS PIDs returned here cannot be passed to `signal_process`.

`signal_process` uses Coder tracked `process_id` only.

#### Safety

read-only / closed-world.

---

## 11. Error output standards

### 11.1 Errors should remain clear text

Tool/domain errors should normally use `isError: true` and actionable `TextContent`. Unlike successful structured results, the explanation itself is the payload the model needs.

Prefer:

```text
File not found: /home/coder/missing.txt
```

over a serialized transport object. Do not add a duplicate structured copy of the same error unless a concrete recovery field cannot be expressed clearly in the text.

### 11.2 Partial failure

Batch read can partially succeed.

Batch edit validation should fail before writes when any requested edit cannot be prepared.

### 11.3 Recovery hints only when useful

Examples:

- process acknowledgement uncertain -> `list_sessions`;
- process still running -> `read_process_output`;
- edit exact match missing -> closest safe candidate/diff;
- paginated result -> next cursor.

Do not add generic boilerplate to every response.

### 11.4 Preserve security boundaries

Never echo:

- API/session tokens;
- SSH private key content;
- `identity_file` in session output;
- process environment values;
- stdin;
- secrets from activity history.

---

## 12. MCP annotations target

The exact code constants may be shared, but the effective intent is:

| Tool                       | readOnly | destructive | idempotent | openWorld |
|----------------------------|---------:|------------:|-----------:|----------:|
| list_workspaces            |     true |       false |       true |     false |
| get_workspace              |     true |       false |       true |     false |
| list_apps                  |     true |       false |       true |     false |
| get_workspace_capabilities |     true |       false |       true |     false |
| list_recent_tool_calls     |     true |       false |       true |     false |
| read_file                  |     true |       false |       true |     false |
| read_multiple_files        |     true |       false |       true |     false |
| get_file_info              |     true |       false |       true |     false |
| list_directory             |     true |       false |       true |     false |
| start_search               |     true |       false |      false |     false |
| get_search_results         |     true |       false |       true |     false |
| list_searches              |     true |       false |       true |     false |
| stop_search                |     true |       false |       true |     false |
| write_file                 |    false |        true |       true |     false |
| edit_file                  |    false |        true |      false |     false |
| edit_multiple_files        |    false |        true |      false |     false |
| create_directory           |    false |       false |       true |     false |
| move_file                  |    false |        true |      false |     false |
| start_process              |    false |        true |      false |      true |
| execute_shell_command      |    false |        true |      false |      true |
| read_process_output        |     true |       false |       true |     false |
| interact_with_process      |    false |        true |      false |      true |
| signal_process             |    false |        true |      false |      true |
| list_sessions              |     true |       false |       true |     false |
| list_processes             |     true |       false |       true |     false |

Note: `start_search` creates only ephemeral search-session state while reading workspace data; it remains read-only with respect to user/workspace data. If the MCP spec/client interprets readOnly strictly as "no server state change of any kind", revisit this one annotation explicitly rather than silently changing unrelated tools.

---

## 13. Public naming and migration

Target public names are the 25 names in section 5.

Historical public names include:

- `status`
- `read_files`
- `file_info`
- `edit_files`
- `search_start`
- `search_results`
- `search_list`
- `search_stop`
- `bash`
- `exec`
- `process_start`
- `process_output`
- `process_input`
- `process_signal`
- `process_list`
- `capabilities`
- `recent_activity`

Rules:

1. Do not expose duplicate old+new names in `tools/list`; this harms model selection and permission UX.
2. Internal SDK constants may keep legacy implementation names.
3. If temporary hidden old-name acceptance is implemented for rolling migration, it must:
   - not appear in `tools/list`;
   - preserve the same permission/safety behavior;
   - have dedicated tests;
   - be documented as transitional.
4. `exec` has no canonical replacement alias with identical behavior. Its intended migration is `start_process(argv=...)` with initial observation.
5. Activity/history should normalize old execution names to the new canonical public names when safe.

---

## 14. Implementation reconciliation status

The human reviewer confirmed this specification on 2026-09-21. The `feat/mcp-api-v2` worktree has since been reconciled to the approved contract and must continue to be treated as subordinate to this document if a later discrepancy is discovered.

The reconciliation now includes:

- the 25-tool public catalog with assistant-facing `exec` removed;
- consistent public `workspace` input naming, including `get_workspace`;
- separate `start_process(argv)` and `execute_shell_command(command)` launch surfaces;
- default observation wait of 0 with explicit positive `wait_timeout_ms` when the assistant wants observation;
- no hidden public result-count defaults or arbitrary public maxima for file, directory, search, activity, session, process-table, or process-output results;
- no hidden public 1 MiB stdin/data cap on the durable process API;
- no hidden directory traversal ceiling or fixed search execution/file-size/line-preview caps;
- deterministic ordering and stable continuation cursors for directories, tracked sessions, OS processes, activity, and search results;
- canonical `structuredContent` plus advertised `outputSchema` for structured results;
- literal opaque file/process payloads in `content[]` with no Coder-generated metadata mixed into those bytes;
- runtime validation of `structuredContent` against the advertised output schema;
- `list_processes` Agent endpoint/tool with real process start time ordering;
- configurable global MCP tool timeout (`CODER_MCP_TOOL_TIMEOUT_MAX`, 280s default);
- SSH execution plumbing and `interrupt` signal support;
- exact edit + unified diff + closest-candidate diagnostics;
- process/session recovery and recent-activity correlation using canonical public handles.

Separate storage/lifecycle safeguards are intentionally not exposed as assistant-selected result limits. Examples include the bounded retained process-output window and cleanup of completed ephemeral search sessions. Those constraints must remain distinguishable from public response limits and must not silently change the meaning of omitted `limit` parameters.

---

## 15. Testing and acceptance requirements

A public tool is not complete until all applicable levels pass.

### 15.1 Unit tests

For every public tool:

- exact public name;
- exact required/optional input schema;
- enum and behavioral defaults;
- proof that result limits have no hidden default or arbitrary public maximum;
- proof that observation waits default to 0;
- annotations;
- validation errors;
- response renderer;
- declared MCP `outputSchema` for every tool returning `structuredContent`;
- runtime validation of `structuredContent` against that output schema;
- exact/compatible `structuredContent` shape;
- structured-only success results use `content: []` rather than a duplicated text rendering;
- golden/snapshot of model-facing `content[]` where applicable;
- opaque file/process payload purity: no Coder metadata injected into raw payload text;
- no structured field/result row is intentionally rendered again in `content`; incidental overlap intrinsic to raw/diff payload is allowed;
- no typed result serialized as JSON inside `TextContent`;
- no secret leakage.

### 15.2 Filesystem-specific

- text read pagination;
- binary/base64;
- empty file;
- large file;
- missing path;
- file vs directory metadata;
- hidden files;
- recursive directory semantics and alphabetical ordering;
- write create/overwrite;
- permission preservation;
- exact edit success;
- replace-all;
- expected-replacement mismatch;
- unified diff;
- actionable exact-match failure;
- multiple-file partial read;
- multiple-edit phase-1 no-write-on-validation-error;
- move file and directory;
- overwrite false/true behavior.

### 15.3 Search-specific

- files/content;
- literal/regex;
- case sensitivity;
- hidden files;
- no result;
- explicit assistant-selected `max_results` behavior and unlimited-by-default behavior;
- deterministic search-result ordering;
- initial results from `start_search`;
- running -> completed transition;
- pagination;
- explicit wait;
- stop;
- reconnect/recovery where applicable.

### 15.4 Process/session-specific

- short direct argv process completing during initial observation;
- long-running process returning initial output + running state;
- shell command;
- no fake exit code while running;
- process ID always returned;
- output cursor continuation;
- rolling-buffer gap;
- explicit wait;
- interactive input + immediate response output;
- EOF/close;
- interrupt;
- terminate;
- kill;
- process group behavior;
- completed session;
- uncertain acknowledgement/recovery;
- reconnect;
- list_sessions vs OS list_processes separation;
- list_processes filter/limit/cursor;
- arbitrary OS PID cannot be mutated through `signal_process`.

### 15.5 SSH-specific

- host;
- user@host;
- OpenSSH alias;
- port;
- identity file;
- invalid relative identity file;
- leading-option host injection;
- whitespace/NUL rejection;
- shell quoting;
- argv quoting;
- remote workdir/env;
- short remote process;
- long remote process;
- interactive remote process where supported;
- interrupt/terminate/kill of tracked remote process;
- failed authentication;
- unreachable host;
- no identity file leakage in outputs/history/session metadata;
- zsh remote account regression test (no readonly `status` variable).

### 15.6 MCP E2E

Must exercise the real assistant path:

```text
tools/list
  -> select public tool
  -> tools/call
  -> model-facing content
```

Verify:

- only the target catalog is listed;
- `exec` is absent;
- descriptions reference canonical public names;
- input schemas and output schemas match runtime;
- `structuredContent` validates against the advertised `outputSchema`;
- annotations match intended safety;
- `structuredContent` matches the documented canonical fields;
- structured-only success tools return `content: []`;
- non-empty `content[]` is limited to documented raw payload, diff, or actionable error text;
- no duplicate table/summary/JSON rendering of structured records appears in `content[]`;
- opaque payloads remain free of Coder-generated headers;
- starter tools return initial state;
- pagination/cursor values are reusable;
- aliases/migration behavior, if any, behaves as documented.

### 15.7 Real acceptance

Before PR readiness, perform real calls against a running workspace using the same MCP path the assistant uses.

At minimum:

- create/read/edit/move temporary files;
- start/read/interact/signal a process;
- search;
- list sessions;
- list OS processes both without a limit and with an explicit assistant-selected limit/cursor;
- SSH execution where test target is available;
- verify activity records.

Clean up temporary artifacts/processes.

### 15.8 Environment blocker policy

Some existing Coder integration tests attempt to start PostgreSQL through Docker and fail inside the workspace because `/var/run/docker.sock` is unavailable.

This is an environment limitation, not a reason to stop all work.

Use:

- targeted package tests;
- Agent endpoint tests that do not require Docker;
- mocks/fakes where appropriate;
- real MCP acceptance against the live workspace;
- GitHub CI for Docker-dependent integration where necessary.

Do not incorrectly mark Docker-dependent tests as passed.

---

## 16. Implementation sequence

Follow this order:

1. Freeze this specification in repository + Memory2.
2. Reconcile current partial branch with the target catalog.
3. Introduce an MCP presentation layer that emits canonical `structuredContent` plus model-facing `content[]` according to section 4.
4. Convert workspace/discovery outputs.
5. Convert filesystem outputs and edit diagnostics.
6. Convert search lifecycle to return initial results.
7. Collapse execution launch surface:
   - remove public `exec`;
   - make `start_process` argv-only;
   - make `execute_shell_command` shell-only;
   - give both initial output/state.
8. Upgrade `interact_with_process` to return post-input output.
9. Finish `list_sessions` / `list_processes` with optional assistant-selected limits and deterministic newest-first ordering.
10. Finish SSH lifecycle and security tests.
11. Complete activity pagination/output.
12. Run unit/integration/E2E acceptance matrix.
13. Reconcile with latest `custom/v2.35.3` (including PR #70).
14. Only when verified: create PR and assign `levkohimins`.
15. Production deployment remains a separate human-approved step.

---

## 17. Definition of done

The redesign is done only when:

- the public catalog contains the agreed 25 tools;
- `exec` is not in the assistant-facing catalog;
- every public tool has a clear MCP output contract using typed `structuredContent`/`outputSchema` and `content[]` only where it carries distinct payload;
- structured/list/action success results do not duplicate themselves as text; `content` is empty when structure alone is sufficient;
- raw file/process payload is kept separate from Coder metadata and is not JSON-escaped by an inner serialization layer;
- durable starters return useful initial state/results;
- running processes never expose a fake exit code;
- process IDs are always available for tracked processes;
- `interact_with_process` returns immediate follow-up output;
- list/search/process tools have no hidden default result limits or arbitrary public maxima; the assistant may explicitly request a limit and use cursors for continuation;
- process/session/activity/filesystem/search ordering follows the deterministic sorting policy in section 6.5;
- `list_sessions` and `list_processes` are clearly distinct;
- SSH execution works without secret leakage or shell-injection bugs;
- exact edit failures are actionable;
- every public tool has unit + applicable integration + MCP E2E coverage;
- actual assistant calls match the documented output;
- no unresolved regression from stable Coder behavior is introduced;
- the PR is based on the current approved `custom/v2.35.3` history;
- no production deploy is performed without explicit user approval.

---

## 18. Short recovery summary for a future assistant

If you are reading only this section:

- The project is redesigning Coder's assistant-facing Remote MCP.
- Desktop Commander 0.2.51 was tested directly as an empirical UX reference, but it is not authoritative where a cleaner AI-first MCP contract is possible.
- Use typed `structuredContent` (with advertised `outputSchema`) for canonical metadata/handles/records. For structured-only success results, use `content: []`; do not add a text table/summary of the same data.
- Use non-empty `TextContent` only for distinct natural payload such as file contents, base64 bytes, stdout/stderr, unified diffs, or actionable error text. For opaque file/process payloads, Coder metadata must stay in `structuredContent`.
- For ordinary text files, do not add redundant `content_encoding: "text"`; for binary base64 payloads use `content_encoding: "base64"`, and add `mime_type` only when known reliably.
- Keep internal Go results typed and add a presentation/rendering layer at the MCP boundary.
- Target catalog is **25 tools**.
- Remove public `exec`.
- Use `start_process(argv)` for structured direct execution.
- Use `execute_shell_command(command)` for explicit `sh -c`.
- Both launch tools return process ID + initial output + current state.
- `interact_with_process` writes stdin and returns newly observed output.
- `start_search` returns search ID + initial results.
- `list_sessions` = MCP/Coder tracked sessions; `list_processes` = OS process table. Both are newest-first and have optional assistant-selected limits with no default limit.
- SSH is optional only on execution tools via `ssh {host, identity_file?, port?}`.
- Filesystem tools remain local/closed-world.
- Result limits are assistant-controlled: no hidden default `limit` and no arbitrary public maximum. Observation waits default to 0.
- Global MCP call ceiling defaults to 280s and never controls process lifetime.
- Edit success should show unified diff; exact-match failure should give actionable closest-match diagnostics.
- Do not blindly trust the current partial worktree; reconcile it to this approved 2026-09-21 spec before PR.
- Do not make a PR until real MCP E2E acceptance passes.
- Never deploy production without explicit approval.
