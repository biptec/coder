# MCP tool selection eval

This command compares alternative Coder Remote MCP tool catalogs before their
handlers are implemented. It measures whether a model selects the intended tool
and arguments for realistic developer and infrastructure tasks.

The eval never invokes the tool selected by the model. It sends only the user
prompt and tool schemas to an OpenAI Responses-compatible model endpoint, then
scores the returned function call.

## Why this exists

Adding tools changes the model's decision space. A tool can be individually
useful and still make the overall catalog worse by creating ambiguity, adding
schema context, or weakening safety boundaries. This eval makes catalog changes
measurable before implementation.

The initial scenario suite is informed by a production history snapshot captured
on 2026-09-13. The snapshot contained 20,286 persisted MCP requests across six
workspaces and 23 tool names. It showed that remote command work is common and
error-prone enough to deserve first-class design attention:

- `exec` launched `ssh` 349 times; 99 command rows had a nonzero exit code.
- `bash` commands contained SSH use 453 times; 64 had a nonzero exit code.
- 42 `bash` commands combined SSH and curl.
- Git was the most common direct `exec` program, with `status` and `diff` the
  most common Git subcommands.

A nonzero command exit code is not automatically a tool-selection error. These
aggregates are used to prioritize scenarios, not to label historical calls as
incorrect.

Raw production MCP inputs, command arguments, outputs, workspace identifiers,
host names, and credentials are not committed to this suite. The checked-in
prompts are synthetic and use placeholder targets such as `edge-1` and
`example.test`.

A small number of historical `exec`, `bash`, and `process_start` request inputs
predate the current JSON persistence format. Any future history importer must
skip or separately handle those rows rather than assuming every historical input
is valid JSON.

## Catalogs

The baseline is not a checked-in JSON snapshot. The command creates an in-memory
Coder MCP server, calls the real `RegisterDeveloperTools` path, performs MCP
`initialize` and `tools/list`, and uses exactly those schemas. This prevents the
eval baseline from drifting away from the product.

Two proposed catalogs are evaluated against that baseline.

The compact `candidate` overlay in `data/candidate.json` adds seven tool names:

- `remote_hosts`
- `copy_path`
- `remove_path`
- `http_fetch`
- `http_request`
- `code_query`
- `code_rename`

Remote operations otherwise extend existing file, search, execution, and process
contracts with an optional `host`. Opaque `search_id` and `process_id` handles
carry the remote target for follow-up operations. `process_list` also accepts an
optional `host` for recovery when a remote process start lost its acknowledgement
before a process ID was received.

The `specialized` overlay in `data/specialized.json` tests the opposite design.
It keeps the local tool contracts unchanged and clones their real schemas into
separate `remote_*` tools. The seven new non-duplicated tools above remain shared
between both proposals. This intentionally makes the specialized catalog much
larger so the eval can measure whether clearer local/remote separation is worth
the additional decision-space and schema cost.

Read-only HTTP fetch and potentially mutating HTTP requests remain separate in
both proposals so hosts can preserve different safety and approval semantics.
Semantic code query and semantic code rename are separate for the same reason.

### Safety annotations

The proposals record MCP safety annotations as part of the contract even though
the Responses function-selection request itself sends only name, description,
and input schema. New tools use conservative annotations:

| Tool | Read only | Destructive | Idempotent | Open world |
| --- | --- | --- | --- | --- |
| `remote_hosts` | yes | no | yes | no |
| `copy_path` | no | yes | no | yes |
| `remove_path` | no | yes | no | yes |
| `http_fetch` | yes | no | yes | yes |
| `http_request` | no | yes | no | yes |
| `code_query` | yes | no | yes | no |
| `code_rename` | no | yes | no | no |

MCP annotations are static per tool. In the compact catalog, adding remote
capability therefore changes `open_world` to true for an existing tool even when
a particular call omits `host`. Follow-up tools that can consume opaque remote
search/process handles are also open-world. In the specialized catalog, the
original local tools keep their existing closed-world annotations while only the
`remote_*` clones become open-world. This safety separation is the primary
architectural advantage of the larger specialized catalog.

## Inspect without calling a model

```sh
go run ./scripts/mcp-tool-eval --list
```

This prints the baseline, compact candidate, and specialized tool counts,
serialized function-schema sizes, and the scenario inventory. It requires no
model credentials.

## Run an eval

Use existing secret injection for the model credential. Do not commit API keys
or write them into scenario files.

```sh
export MCP_TOOL_EVAL_MODEL='<model>'
export MCP_TOOL_EVAL_API_KEY='<credential>'

go run ./scripts/mcp-tool-eval \
  --repeat 5 \
  --output /tmp/mcp-tool-eval.json
```

The default endpoint is `https://api.openai.com/v1`. An OpenAI
Responses-compatible gateway can be used instead:

```sh
export MCP_TOOL_EVAL_BASE_URL='https://gateway.example.test/v1'
```

If the gateway authenticates out of band and requires no Authorization header,
pass `--api-key-env=-`.

Useful focused runs:

```sh
go run ./scripts/mcp-tool-eval --filter remote --repeat 10
go run ./scripts/mcp-tool-eval --filter remote --catalogs candidate,specialized --repeat 10
go run ./scripts/mcp-tool-eval --filter http --catalogs candidate --repeat 5
go run ./scripts/mcp-tool-eval --filter code --verbose --repeat 3
```

The JSON report is written with mode `0600` because model responses can contain
arguments derived from prompts.

## Scoring

Each selection is scored on:

1. expected tool name;
2. expected argument subset;
3. argument adherence to the selected catalog schema;
4. forbidden fallback tools for scenarios where they would defeat the purpose
   of the structured capability.

Argument schema adherence intentionally rejects undeclared object properties.
Coder's Go handlers decode request arguments into structs, and Go JSON decoding
ignores unknown fields by default. Without this stricter eval check, an old
catalog could incorrectly receive credit for a model-generated `host` argument
that the old handler would silently ignore.

The report also records provider input/output token counts when available,
latency, catalog tool count, and serialized schema bytes.

Model results are comparative evidence, not deterministic CI assertions. Run
multiple repetitions and compare the same model and prompt suite across catalogs.
Unit tests cover the deterministic catalog generation, overlay, scoring, and
Responses protocol behavior.

## History-derived scenarios

`data/scenarios.json` records a `source` for every scenario. Sources beginning
with `production-history:` indicate a scenario derived from observed tool or
command shapes. They do not contain copied production payloads. Sources beginning
with `workflow-guidance:` cover important behavior required by repository
engineering guidance, such as LSP-first semantic code navigation.

`history.sql` contains only aggregate queries used to understand the production
distribution. It is intentionally incapable of returning raw MCP input or command
output.
