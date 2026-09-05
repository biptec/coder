# MCP Server

Coder includes a built-in [Model Context Protocol](https://modelcontextprotocol.io/)
(MCP) server that provides AI assistants with tools and context about your Coder
deployment. This enables AI-powered workflows for managing workspaces,
templates, and development environments.

Coder supports two MCP server modes:

- **[Local MCP Server](#local-mcp-server)**: Runs via the Coder CLI using stdio
  transport. Ideal for local AI tools and IDE integrations.
- **[Remote MCP Server](#remote-mcp-server)**: HTTP-based server exposed by your
  Coder deployment. Supports OAuth2 authentication and is published to the MCP
  Registry.

## Local MCP Server

The local MCP server runs via the Coder CLI and uses stdio transport to
communicate with AI tools.

### Setup

Run the MCP server using the Coder CLI:

```sh
coder exp mcp server
```

### Client Configuration

Configure your MCP client to spawn the Coder CLI:

```json
{
  "mcpServers": {
    "coder": {
      "command": "coder",
      "args": ["exp", "mcp", "server"]
    }
  }
}
```

The CLI automatically uses your existing Coder authentication (from `coder login`).

### Claude Desktop Example

Add to your Claude Desktop configuration file:

<div class="tabs">

#### macOS

Edit `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "coder": {
      "command": "coder",
      "args": ["exp", "mcp", "server"]
    }
  }
}
```

#### Windows

Edit `%APPDATA%\Claude\claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "coder": {
      "command": "coder.exe",
      "args": ["exp", "mcp", "server"]
    }
  }
}
```

</div>

## Remote MCP Server

The remote MCP server is an HTTP endpoint exposed by your Coder deployment at
`/api/experimental/mcp/http`. This enables MCP clients to connect to Coder
without running the CLI locally.

### Prerequisites

The remote MCP HTTP endpoint requires both the `oauth2` and `mcp-server-http`
experiments enabled on your Coder deployment:

```sh
coder server --experiments=oauth2,mcp-server-http
```

Or set the environment variable:

```sh
CODER_EXPERIMENTS=oauth2,mcp-server-http
```

### MCP Registry

Coder is published to the official [MCP Registry](https://github.com/modelcontextprotocol/registry)
as `io.github.coder/coder`, enabling easy installation in supported MCP clients.

#### VS Code / GitHub Copilot

1. Open VS Code Command Palette and run **MCP: Add Server...**
1. Select **From MCP Registry**
1. Search for "Coder" and select it
1. Enter your Coder deployment hostname when prompted (e.g., `coder.example.com`)
1. VS Code will automatically handle OAuth2 authentication

#### Claude Desktop (Remote)

Add to your Claude Desktop configuration file (`claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "coder": {
      "url": "https://coder.example.com/api/experimental/mcp/http"
    }
  }
}
```

Claude Desktop will automatically discover OAuth2 endpoints and prompt you to
authenticate through your browser.

### Manual Configuration

For MCP clients that don't support the registry or OAuth2 discovery, configure
the server manually with a session token:

```json
{
  "mcpServers": {
    "coder": {
      "url": "https://coder.example.com/api/experimental/mcp/http",
      "headers": {
        "Coder-Session-Token": "<your-session-token>"
      }
    }
  }
}
```

To create a session token:

1. Navigate to your Coder deployment
1. Go to **Settings > Tokens**
1. Create a new token
1. Add the token to your MCP client configuration

## Authentication

The MCP server supports two authentication methods:

### OAuth2 (Recommended for Interactive Clients)

MCP clients that support [RFC 9728](https://datatracker.ietf.org/doc/html/rfc9728)
(Protected Resource Metadata) can authenticate automatically using OAuth2. The
server advertises its OAuth2 capabilities via the `WWW-Authenticate` header and
`/.well-known/oauth-protected-resource` endpoint.

This enables a seamless "click-to-connect" experience where users authenticate
through their browser without manually managing tokens.

> [!NOTE]
> OAuth2 requires the `oauth2` experiment to be enabled on your Coder deployment.

### Session Token (For Programmatic Access)

For clients that don't support OAuth2 discovery, or for programmatic access, use
a session token as shown in the [Manual Configuration](#manual-configuration)
section.

## Available Tools

Remote MCP tools are selected server-side from the toolset assigned to the
authenticated Coder user. The API or OAuth token identifies the user; changing
query parameters cannot upgrade the assigned toolset.

### Developer toolset

The developer toolset uses concise, assistant-facing names and focuses on safe
workspace development workflows:

- Workspace discovery: `status`, `list_workspaces`
- Files: `list_directory`, `read_file`, `read_files`, `write_file`, `file_info`,
  `create_directory`, `move_file`, `edit_file`, `edit_files`
- Search: `search_start`, `search_results`, `search_list`, `search_stop`
- Commands: `exec`, `bash`
- Durable processes: `process_start`, `process_output`, `process_list`,
  `process_input`, `process_signal`
- Workspace apps and recovery: `list_apps`, `recent_activity`

Prefer `exec` when a program can be expressed as an executable plus an argument
array. Its `argv` values are passed directly to the workspace agent without
shell parsing, so quotes, JSON, SQL, JSONPath expressions, and other arguments
do not need another shell-escaping layer. Use `bash` only when shell syntax is
intentional, such as pipelines, redirects, compound commands, or loops.

`process_start` is the durable equivalent for long-running or side-effecting
work. It accepts exactly one of `argv` (direct execution) or `command` (shell
execution). Set `interactive=true` only when later `process_input` calls are
needed. Initial and incremental stdin payloads are limited to 1 MiB per call.

`process_output` supports a byte cursor for incremental reads. Start with
`cursor=0` and continue from `next_cursor`. The agent keeps a bounded rolling
output window; if a client falls behind, `gap_bytes` explicitly reports output
that was evicted instead of silently presenting the remaining bytes as a
complete stream. Omitting the cursor preserves the legacy bounded head/tail
snapshot.

`read_file` is text-first: by default, `offset` is a 1-based line number and the
response contains line-numbered text. Set `binary=true` to use byte offsets and
receive base64. `read_files` batches up to 20 reads and reports errors per file.
`list_directory` supports bounded recursion, metadata, hidden-file control, and
pagination. Directory recursion never follows symlinks.

`edit_file` and `edit_files` retain Coder's whitespace- and indentation-aware
matching. `expected_replacements` can be used as a fail-closed precondition: if
the actual match count differs, the file is not written. Successful edit
responses include the match mode, replacement count, and unified diff.

Workspace search is session-based. `search_start` supports file-name or content
search, literal or Go RE2 expressions, and returns a `search_id` immediately.
Results are retrieved with `search_results`; `search_list` and `search_stop`
provide recovery and cancellation. Search sessions are bounded by result, file,
line, execution-time, and retention limits so searches cannot grow without
bound.

`recent_activity` stores only bounded recovery metadata for the authenticated
Coder user: tool name, workspace, status, timestamps, duration, and returned
process/search IDs. It retains the latest 100 completed records plus any calls
that are still running. It does not store command output, file contents,
environment variables, stdin, tokens, or secret values. This history is
in-memory recovery state and resets when the Coder server process restarts.

### Readonly toolset

The readonly toolset exposes workspace discovery, file/directory reads,
metadata, search sessions, process observation, apps, and `recent_activity`.
It does not expose file mutations, edit operations, command execution, process
start/input/signal, or other workspace mutations.

### Admin toolset

The admin toolset preserves the full legacy Remote MCP catalog, including the
existing `coder_*` tool names for workspace, template, task, user, and system
administration. Assistant-facing Phase 2 tools are kept separate from this
legacy catalog where changing the old schema would break compatibility. For
example, the legacy `coder_workspace_process_start` remains command-only while
the developer `process_start` adds direct `argv`, interactive stdin, and related
Phase 2 behavior.

The authoritative tool definitions live in Coder's
[`toolsdk` package](../../codersdk/toolsdk/toolsdk.go) and the Remote MCP
registration in [`coderd/mcp`](../../coderd/mcp). Available tools can change
between releases.

## Troubleshooting

### "Unauthorized" errors

- Verify your session token is valid and not expired
- Check that the MCP server experiment is enabled on your deployment
- Ensure your user has appropriate permissions for the requested operations

### Connection timeouts

- Verify your Coder deployment URL is correct and accessible
- Check network connectivity between your MCP client and the Coder server
- Review Coder server logs for any errors

### OAuth2 authentication not working

- Ensure your Coder deployment has the `oauth2` experiment enabled
- Verify your MCP client supports RFC 9728 Protected Resource Metadata
- Check that your browser can reach the Coder authorization endpoint
