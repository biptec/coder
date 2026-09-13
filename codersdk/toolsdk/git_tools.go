package toolsdk

import (
	"context"
	"encoding/base64"
	"net/url"
	"path"
	"strconv"
	"strings"

	"golang.org/x/xerrors"

	"github.com/coder/aisdk-go"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const maxGitToolOutputBytes = 256 << 10

type WorkspaceGitQueryArgs struct {
	Workspace string   `json:"workspace"`
	Repo      string   `json:"repo"`
	Operation string   `json:"operation"`
	Ref       string   `json:"ref,omitempty"`
	Query     string   `json:"query,omitempty"`
	Staged    bool     `json:"staged,omitempty"`
	Limit     int      `json:"limit,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	Host      string   `json:"host,omitempty"`
}

type WorkspaceGitResult struct {
	Output    string `json:"output"`
	Truncated bool   `json:"truncated,omitempty"`
}

var WorkspaceGitQuery = Tool[WorkspaceGitQueryArgs, WorkspaceGitResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceGitQuery,
		Description: `Query a local Git repository without modifying it. This tool covers the high-frequency read-only Git operations that otherwise require exec.

Supported operations:
- status: stable short status with branch information
- diff: unstaged diff, staged diff when staged=true, or diff against ref
- log: bounded commit log; ref is optional
- show: show ref (defaults to HEAD)
- grep: repository search; query is required and ref is optional
- branch: list local branches with tracking information
- worktree_list: porcelain worktree list
- remote: list configured remotes without contacting the network
- rev_parse: verify and resolve ref

Network Git operations such as fetch, pull, and push intentionally remain in exec because they cross a separate credential/open-world boundary.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"repo":      map[string]any{"type": "string", "description": "Absolute repository path."},
				"operation": map[string]any{
					"type":        "string",
					"description": "Read-only Git operation.",
					"enum":        []string{"status", "diff", "log", "show", "grep", "branch", "worktree_list", "remote", "rev_parse"},
				},
				"ref":    map[string]any{"type": "string", "description": "Optional revision/ref/range used by diff, log, show, grep, or rev_parse."},
				"query":  map[string]any{"type": "string", "description": "Pattern required by grep."},
				"staged": map[string]any{"type": "boolean", "description": "For diff, compare staged changes instead of unstaged changes."},
				"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "description": "Maximum commits for log. Defaults to 50."},
				"paths": map[string]any{
					"type":        "array",
					"description": "Optional repository-relative pathspecs for diff/log/show/grep.",
					"items":       map[string]any{"type": "string"},
					"maxItems":    100,
				},
				"host": map[string]any{"type": "string", "description": "Optional SSH alias returned by remote_hosts."},
			},
			Required: []string{"workspace", "repo", "operation"},
		},
	},
	MCPAnnotations:     mcpReadOnlyOpenWorldAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceGitQueryArgs) (WorkspaceGitResult, error) {
		argv, err := gitQueryCommand(args)
		if err != nil {
			return WorkspaceGitResult{}, err
		}
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return WorkspaceGitResult{}, err
		}
		defer conn.Close()
		resp, err := conn.RunCommand(ctx, workspacesdk.RunCommandRequest{Host: args.Host, Argv: argv})
		if err != nil {
			return WorkspaceGitResult{}, xerrors.Errorf("git %s: %w", args.Operation, err)
		}
		output, err := base64.StdEncoding.DecodeString(resp.StdoutBase64)
		if err != nil {
			return WorkspaceGitResult{}, xerrors.Errorf("decode git output: %w", err)
		}
		if resp.ExitCode != 0 && !(args.Operation == "grep" && resp.ExitCode == 1) {
			detail := strings.TrimSpace(resp.Stderr)
			if detail == "" {
				detail = "git exited with code " + strconv.Itoa(resp.ExitCode)
			}
			return WorkspaceGitResult{}, xerrors.Errorf("git %s failed: %s", args.Operation, detail)
		}
		if args.Operation == "remote" {
			output = []byte(sanitizeGitRemoteOutput(string(output)))
		}
		text, truncated := truncateSemanticOutput(output, maxGitToolOutputBytes)
		return WorkspaceGitResult{Output: text, Truncated: truncated}, nil
	},
}

func gitQueryCommand(args WorkspaceGitQueryArgs) ([]string, error) {
	if err := validateGitCommon(args.Repo, args.Host); err != nil {
		return nil, err
	}
	if err := validateGitRef(args.Ref); err != nil {
		return nil, err
	}
	if len(args.Paths) > 100 {
		return nil, xerrors.New("paths cannot contain more than 100 entries")
	}
	base := []string{
		"git", "--no-optional-locks",
		"-c", "core.fsmonitor=false",
		"-c", "core.hooksPath=/dev/null",
		"-C", args.Repo, "--no-pager",
	}
	operation := strings.TrimSpace(args.Operation)
	switch operation {
	case "status":
		return append(base, "status", "--short", "--branch", "--untracked-files=all", "--ignore-submodules=all"), nil
	case "diff":
		argv := append([]string{}, base...)
		argv = append(argv, "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--ignore-submodules=all")
		if args.Staged {
			argv = append(argv, "--cached")
		}
		if args.Ref != "" {
			argv = append(argv, args.Ref)
		}
		return appendGitPaths(argv, args.Paths), nil
	case "log":
		limit := args.Limit
		if limit == 0 {
			limit = 50
		}
		if limit < 1 || limit > 200 {
			return nil, xerrors.New("limit must be between 1 and 200")
		}
		argv := append([]string{}, base...)
		argv = append(argv, "log", "--no-color", "--decorate=short", "--date=iso-strict", "--format=medium", "-n", strconv.Itoa(limit))
		if args.Ref != "" {
			argv = append(argv, args.Ref)
		}
		return appendGitPaths(argv, args.Paths), nil
	case "show":
		ref := args.Ref
		if ref == "" {
			ref = "HEAD"
		}
		argv := append([]string{}, base...)
		argv = append(argv, "show", "--no-ext-diff", "--no-textconv", "--no-color", "--ignore-submodules=all", "--format=fuller", ref)
		return appendGitPaths(argv, args.Paths), nil
	case "grep":
		if args.Query == "" {
			return nil, xerrors.New("query is required for grep")
		}
		argv := append([]string{}, base...)
		argv = append(argv, "grep", "-n", "--full-name", "-I", "-e", args.Query)
		if args.Ref != "" {
			argv = append(argv, args.Ref)
		}
		return appendGitPaths(argv, args.Paths), nil
	case "branch":
		return append(base, "branch", "--list", "-vv", "--no-color"), nil
	case "worktree_list":
		return append(base, "worktree", "list", "--porcelain"), nil
	case "remote":
		return append(base, "remote", "-v"), nil
	case "rev_parse":
		if args.Ref == "" {
			return nil, xerrors.New("ref is required for rev_parse")
		}
		return append(base, "rev-parse", "--verify", args.Ref), nil
	default:
		return nil, xerrors.Errorf("unsupported git_query operation %q", operation)
	}
}

type WorkspaceGitMutateArgs struct {
	Workspace string   `json:"workspace"`
	Repo      string   `json:"repo"`
	Operation string   `json:"operation"`
	Paths     []string `json:"paths,omitempty"`
	Message   string   `json:"message,omitempty"`
	Branch    string   `json:"branch,omitempty"`
	Ref       string   `json:"ref,omitempty"`
	Mode      string   `json:"mode,omitempty"`
	Source    string   `json:"source,omitempty"`
	Staged    bool     `json:"staged,omitempty"`
	Worktree  string   `json:"worktree,omitempty"`
	NewBranch string   `json:"new_branch,omitempty"`
	Force     bool     `json:"force,omitempty"`
	Host      string   `json:"host,omitempty"`
}

var WorkspaceGitMutate = Tool[WorkspaceGitMutateArgs, WorkspaceGitResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceGitMutate,
		Description: `Perform a structured local Git mutation without network access.

Supported operations:
- add: stage paths
- commit: create a commit with message
- create_branch: create branch, optionally at ref
- checkout: checkout branch/ref
- switch: switch to an existing branch
- restore: restore paths; staged=true restores the index, source optionally selects a tree
- reset: reset to ref (defaults HEAD) using mode soft, mixed, or hard
- worktree_add: add a worktree at absolute worktree path, optionally with ref or new_branch
- worktree_remove: remove an existing worktree, optionally force=true

This tool never performs fetch/pull/push. Use dry semantic/read tools before destructive operations when appropriate.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"repo":      map[string]any{"type": "string", "description": "Absolute repository path."},
				"operation": map[string]any{
					"type":        "string",
					"description": "Git mutation to perform.",
					"enum":        []string{"add", "commit", "create_branch", "checkout", "switch", "restore", "reset", "worktree_add", "worktree_remove"},
				},
				"paths":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 100, "description": "Repository-relative paths used by add or restore."},
				"message":    map[string]any{"type": "string", "description": "Commit message required by commit."},
				"branch":     map[string]any{"type": "string", "description": "Branch required by create_branch or switch."},
				"ref":        map[string]any{"type": "string", "description": "Optional revision/start point used by create_branch, checkout, reset, or worktree_add."},
				"mode":       map[string]any{"type": "string", "enum": []string{"soft", "mixed", "hard"}, "description": "Reset mode. Defaults to mixed."},
				"source":     map[string]any{"type": "string", "description": "Optional restore source revision."},
				"staged":     map[string]any{"type": "boolean", "description": "For restore, restore the index instead of only the worktree."},
				"worktree":   map[string]any{"type": "string", "description": "Absolute worktree path for worktree_add/worktree_remove."},
				"new_branch": map[string]any{"type": "string", "description": "Optional new branch created by worktree_add."},
				"force":      map[string]any{"type": "boolean", "description": "Allow forced worktree removal."},
				"host":       map[string]any{"type": "string", "description": "Optional SSH alias returned by remote_hosts."},
			},
			Required: []string{"workspace", "repo", "operation"},
		},
	},
	MCPAnnotations:     mcpDestructiveOpenWorldAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceGitMutateArgs) (WorkspaceGitResult, error) {
		argv, err := gitMutateCommand(args)
		if err != nil {
			return WorkspaceGitResult{}, err
		}
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return WorkspaceGitResult{}, err
		}
		defer conn.Close()
		output, err := runHelperCommand(ctx, conn, workspacesdk.RunCommandRequest{Host: args.Host, Argv: argv})
		if err != nil {
			return WorkspaceGitResult{}, xerrors.Errorf("git %s: %w", args.Operation, err)
		}
		text, truncated := truncateSemanticOutput(output, maxGitToolOutputBytes)
		return WorkspaceGitResult{Output: text, Truncated: truncated}, nil
	},
}

func gitMutateCommand(args WorkspaceGitMutateArgs) ([]string, error) {
	if err := validateGitCommon(args.Repo, args.Host); err != nil {
		return nil, err
	}
	for name, value := range map[string]string{"ref": args.Ref, "branch": args.Branch, "source": args.Source, "new_branch": args.NewBranch} {
		if value != "" {
			if err := validateGitNamedValue(name, value); err != nil {
				return nil, err
			}
		}
	}
	if len(args.Paths) > 100 {
		return nil, xerrors.New("paths cannot contain more than 100 entries")
	}
	base := []string{"git", "-C", args.Repo, "--no-pager"}
	switch strings.TrimSpace(args.Operation) {
	case "add":
		if len(args.Paths) == 0 {
			return nil, xerrors.New("paths are required for add")
		}
		return appendGitPaths(append(base, "add"), args.Paths), nil
	case "commit":
		if strings.TrimSpace(args.Message) == "" {
			return nil, xerrors.New("message is required for commit")
		}
		return append(base, "commit", "-m", args.Message), nil
	case "create_branch":
		if args.Branch == "" {
			return nil, xerrors.New("branch is required for create_branch")
		}
		argv := append([]string{}, base...)
		argv = append(argv, "branch", args.Branch)
		if args.Ref != "" {
			argv = append(argv, args.Ref)
		}
		return argv, nil
	case "checkout":
		if args.Ref == "" {
			return nil, xerrors.New("ref is required for checkout")
		}
		return append(base, "checkout", args.Ref), nil
	case "switch":
		if args.Branch == "" {
			return nil, xerrors.New("branch is required for switch")
		}
		return append(base, "switch", args.Branch), nil
	case "restore":
		if len(args.Paths) == 0 {
			return nil, xerrors.New("paths are required for restore")
		}
		argv := append([]string{}, base...)
		argv = append(argv, "restore")
		if args.Staged {
			argv = append(argv, "--staged")
		}
		if args.Source != "" {
			argv = append(argv, "--source", args.Source)
		}
		return appendGitPaths(argv, args.Paths), nil
	case "reset":
		mode := strings.TrimSpace(args.Mode)
		if mode == "" {
			mode = "mixed"
		}
		if mode != "soft" && mode != "mixed" && mode != "hard" {
			return nil, xerrors.New("mode must be soft, mixed, or hard")
		}
		ref := args.Ref
		if ref == "" {
			ref = "HEAD"
		}
		return append(base, "reset", "--"+mode, ref), nil
	case "worktree_add":
		if args.Worktree == "" || !path.IsAbs(args.Worktree) {
			return nil, xerrors.New("worktree must be an absolute path for worktree_add")
		}
		argv := append([]string{}, base...)
		argv = append(argv, "worktree", "add")
		if args.NewBranch != "" {
			argv = append(argv, "-b", args.NewBranch)
		}
		argv = append(argv, args.Worktree)
		if args.Ref != "" {
			argv = append(argv, args.Ref)
		}
		return argv, nil
	case "worktree_remove":
		if args.Worktree == "" || !path.IsAbs(args.Worktree) {
			return nil, xerrors.New("worktree must be an absolute path for worktree_remove")
		}
		argv := append([]string{}, base...)
		argv = append(argv, "worktree", "remove")
		if args.Force {
			argv = append(argv, "--force")
		}
		return append(argv, args.Worktree), nil
	default:
		return nil, xerrors.Errorf("unsupported git_mutate operation %q", args.Operation)
	}
}

func validateGitCommon(repo, host string) error {
	if repo == "" || !path.IsAbs(repo) {
		return xerrors.New("repo must be an absolute path")
	}
	return validateRemoteTarget(host, "")
}

func validateGitRef(ref string) error {
	if ref == "" {
		return nil
	}
	return validateGitNamedValue("ref", ref)
}

func validateGitNamedValue(name, value string) error {
	if strings.ContainsAny(value, "\x00\r\n") {
		return xerrors.Errorf("%s cannot contain NUL or newline characters", name)
	}
	if strings.HasPrefix(value, "-") {
		return xerrors.Errorf("%s cannot begin with '-'", name)
	}
	return nil
}

func appendGitPaths(argv, paths []string) []string {
	if len(paths) == 0 {
		return argv
	}
	argv = append(argv, "--")
	return append(argv, paths...)
}

func sanitizeGitRemoteOutput(output string) string {
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		name, value, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		suffix := ""
		for _, candidate := range []string{" (fetch)", " (push)"} {
			if strings.HasSuffix(value, candidate) {
				suffix = candidate
				value = strings.TrimSuffix(value, candidate)
				break
			}
		}
		lines[i] = name + "\t" + sanitizeGitRemoteURL(value) + suffix
	}
	return strings.Join(lines, "\n")
}

func sanitizeGitRemoteURL(rawURL string) string {
	// Git remote helpers use transport::address and can contain arbitrary helper
	// arguments. Do not echo those back through a read-only metadata tool.
	if strings.Contains(rawURL, "::") {
		return "<redacted>"
	}

	// Handle SCP-like remotes before url.Parse, because strings such as
	// git@github.com:owner/repo.git are not RFC URLs.
	if !strings.Contains(rawURL, "://") {
		if colon := strings.IndexByte(rawURL, ':'); colon > 0 && !strings.Contains(rawURL[:colon], "/") {
			host := rawURL[:colon]
			if at := strings.LastIndexByte(host, '@'); at >= 0 {
				host = host[at+1:]
			}
			return host + rawURL[colon:]
		}
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" {
		return rawURL
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "ssh", "git", "file":
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.ForceQuery = false
		parsed.Fragment = ""
		return parsed.String()
	default:
		return "<redacted>"
	}
}

func truncateSemanticOutput(data []byte, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(data) <= maxBytes {
		return strings.ToValidUTF8(string(data), "�"), false
	}
	marker := []byte("\n... [output truncated by MCP tool] ...\n")
	if maxBytes <= len(marker)+2 {
		return strings.ToValidUTF8(string(data[:maxBytes]), "�"), true
	}
	head := (maxBytes - len(marker)) * 3 / 4
	tail := maxBytes - len(marker) - head
	out := make([]byte, 0, maxBytes)
	out = append(out, data[:head]...)
	out = append(out, marker...)
	out = append(out, data[len(data)-tail:]...)
	return strings.ToValidUTF8(string(out), "�"), true
}
