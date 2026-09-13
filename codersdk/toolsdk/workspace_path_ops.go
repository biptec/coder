package toolsdk

import (
	"context"
	"path"
	"strings"

	"golang.org/x/xerrors"

	"github.com/coder/aisdk-go"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

type WorkspacePathEndpoint struct {
	Path         string `json:"path"`
	Host         string `json:"host,omitempty"`
	IdentityFile string `json:"identity_file,omitempty"`
}

func validateRemoteTarget(host, identityFile string) error {
	host = strings.TrimSpace(host)
	if identityFile != "" {
		return xerrors.New("identity_file is not accepted by assistant remote tools; configure IdentityFile on the SSH alias and call remote_hosts to discover allowed targets")
	}
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return xerrors.New("host must be an exact SSH alias containing only letters, digits, '.', '_' or '-'; call remote_hosts to list allowed targets")
		}
	}
	return nil
}

func validateConfiguredRemoteAliases(ctx context.Context, conn workspacesdk.AgentConn, hosts ...string) error {
	needValidation := false
	for _, host := range hosts {
		if strings.TrimSpace(host) != "" {
			needValidation = true
			break
		}
	}
	if !needValidation {
		return nil
	}
	response, err := conn.ListRemoteHosts(ctx)
	if err != nil {
		return xerrors.Errorf("list configured SSH aliases: %w", err)
	}
	allowed := make(map[string]struct{}, len(response.Hosts))
	for _, host := range response.Hosts {
		allowed[strings.ToLower(host.Alias)] = struct{}{}
	}
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		if _, ok := allowed[strings.ToLower(host)]; !ok {
			return xerrors.Errorf("SSH alias %q is not configured; call remote_hosts to list allowed targets", host)
		}
	}
	return nil
}

func validatePathEndpoint(endpoint WorkspacePathEndpoint) error {
	if endpoint.Path == "" || !path.IsAbs(endpoint.Path) {
		return xerrors.Errorf("path must be absolute: %q", endpoint.Path)
	}
	return validateRemoteTarget(endpoint.Host, endpoint.IdentityFile)
}

func endpointSSHInvocation(endpoint WorkspacePathEndpoint, remoteCommand string) (string, error) {
	if err := validatePathEndpoint(endpoint); err != nil {
		return "", err
	}
	if endpoint.Host == "" {
		return remoteCommand, nil
	}
	parts := []string{"ssh", "-o", "BatchMode=yes"}
	if endpoint.IdentityFile != "" {
		parts = append(parts, "-i", endpoint.IdentityFile)
	}
	parts = append(parts, "--", strings.TrimSpace(endpoint.Host), remoteCommand)
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		quoted = append(quoted, toolShellQuote(part))
	}
	return strings.Join(quoted, " "), nil
}

func samePathTarget(a, b WorkspacePathEndpoint) bool {
	return strings.EqualFold(strings.TrimSpace(a.Host), strings.TrimSpace(b.Host)) && a.IdentityFile == b.IdentityFile
}

func pathAncestorOf(parent, child string) bool {
	parent = path.Clean(parent)
	child = path.Clean(child)
	if parent == child {
		return false
	}
	if parent == "/" {
		return true
	}
	return strings.HasPrefix(child, parent+"/")
}

type WorkspaceCopyPathArgs struct {
	Workspace   string                `json:"workspace"`
	Source      WorkspacePathEndpoint `json:"source"`
	Destination WorkspacePathEndpoint `json:"destination"`
	Overwrite   bool                  `json:"overwrite,omitempty"`
}

var WorkspaceCopyPath = Tool[WorkspaceCopyPathArgs, codersdk.Response]{
	Tool: aisdk.Tool{
		Name:        ToolNameWorkspaceCopyPath,
		Description: `Copy a file, directory, or symlink between workspace-local and/or SSH targets. Source and destination paths are exact paths; this tool does not copy a source inside an existing destination directory.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"source": map[string]any{
					"type":        "object",
					"description": "Source path and optional SSH alias returned by remote_hosts.",
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": "Absolute source path."},
						"host": map[string]any{"type": "string", "description": "Optional SSH alias returned by remote_hosts."},
					},
					"required": []string{"path"},
				},
				"destination": map[string]any{
					"type":        "object",
					"description": "Destination path and optional SSH alias returned by remote_hosts.",
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": "Absolute destination path."},
						"host": map[string]any{"type": "string", "description": "Optional SSH alias returned by remote_hosts."},
					},
					"required": []string{"path"},
				},
				"overwrite": map[string]any{"type": "boolean", "description": "Replace an existing destination path. Defaults to false."},
			},
			Required: []string{"workspace", "source", "destination"},
		},
	},
	MCPAnnotations:     mcpDestructiveOpenWorldAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceCopyPathArgs) (codersdk.Response, error) {
		if err := validatePathEndpoint(args.Source); err != nil {
			return codersdk.Response{}, xerrors.Errorf("source: %w", err)
		}
		if err := validatePathEndpoint(args.Destination); err != nil {
			return codersdk.Response{}, xerrors.Errorf("destination: %w", err)
		}
		if path.Clean(args.Source.Path) == "/" {
			return codersdk.Response{}, xerrors.New("copying filesystem root is not supported")
		}
		if path.Clean(args.Destination.Path) == "/" {
			return codersdk.Response{}, xerrors.New("destination cannot be filesystem root")
		}
		if samePathTarget(args.Source, args.Destination) && path.Clean(args.Source.Path) == path.Clean(args.Destination.Path) {
			return codersdk.Response{Message: "Source and destination are identical."}, nil
		}
		if samePathTarget(args.Source, args.Destination) && (pathAncestorOf(args.Source.Path, args.Destination.Path) || pathAncestorOf(args.Destination.Path, args.Source.Path)) {
			return codersdk.Response{}, xerrors.New("source and destination cannot be ancestor/descendant paths on the same target")
		}

		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return codersdk.Response{}, err
		}
		defer conn.Close()
		if err := validateConfiguredRemoteAliases(ctx, conn, args.Source.Host, args.Destination.Host); err != nil {
			return codersdk.Response{}, err
		}

		if samePathTarget(args.Source, args.Destination) {
			command := "if [ ! -e " + toolShellQuote(args.Source.Path) + " ] && [ ! -L " + toolShellQuote(args.Source.Path) + " ]; then echo 'source does not exist' >&2; exit 2; fi; " +
				"if [ -e " + toolShellQuote(args.Destination.Path) + " ] || [ -L " + toolShellQuote(args.Destination.Path) + " ]; then "
			if !args.Overwrite {
				command += "echo 'destination exists' >&2; exit 17; "
			} else {
				command += "rm -rf -- " + toolShellQuote(args.Destination.Path) + "; "
			}
			command += "fi; cp -a -- " + toolShellQuote(args.Source.Path) + " " + toolShellQuote(args.Destination.Path)
			_, err = runHelperCommand(ctx, conn, workspacesdk.RunCommandRequest{
				Host:         args.Source.Host,
				IdentityFile: args.Source.IdentityFile,
				Command:      command,
			})
			if err != nil {
				return codersdk.Response{}, xerrors.Errorf("copy path: %w", err)
			}
			return codersdk.Response{Message: "Path copied."}, nil
		}

		sourceParent := path.Dir(path.Clean(args.Source.Path))
		sourceBase := path.Base(path.Clean(args.Source.Path))
		sourceCommand := "if [ ! -e " + toolShellQuote(args.Source.Path) + " ] && [ ! -L " + toolShellQuote(args.Source.Path) + " ]; then echo 'source does not exist' >&2; exit 2; fi; tar -C " + toolShellQuote(sourceParent) + " -cf - -- " + toolShellQuote(sourceBase)
		sourceInvocation, err := endpointSSHInvocation(args.Source, sourceCommand)
		if err != nil {
			return codersdk.Response{}, err
		}

		destinationParent := path.Dir(path.Clean(args.Destination.Path))
		destinationCommand := "set -eu; src_base=" + toolShellQuote(sourceBase) + "; test -d " + toolShellQuote(destinationParent) + "; " +
			"tmp=$(mktemp -d -- " + toolShellQuote(path.Join(destinationParent, ".coder-copy.XXXXXX")) + "); " +
			"cleanup(){ rm -rf -- \"$tmp\"; }; trap cleanup EXIT HUP INT TERM; " +
			"tar -xf - -C \"$tmp\"; "
		if args.Overwrite {
			destinationCommand += "if [ -e " + toolShellQuote(args.Destination.Path) + " ] || [ -L " + toolShellQuote(args.Destination.Path) + " ]; then rm -rf -- " + toolShellQuote(args.Destination.Path) + "; fi; "
		} else {
			destinationCommand += "if [ -e " + toolShellQuote(args.Destination.Path) + " ] || [ -L " + toolShellQuote(args.Destination.Path) + " ]; then echo 'destination exists' >&2; exit 17; fi; "
		}
		destinationCommand += "mv -- \"$tmp/$src_base\" " + toolShellQuote(args.Destination.Path) + "; trap - EXIT HUP INT TERM; rmdir -- \"$tmp\""
		destinationInvocation, err := endpointSSHInvocation(args.Destination, destinationCommand)
		if err != nil {
			return codersdk.Response{}, err
		}

		pipeline := sourceInvocation + " | " + destinationInvocation
		_, err = runHelperCommand(ctx, conn, workspacesdk.RunCommandRequest{Argv: []string{"bash", "-o", "pipefail", "-c", pipeline}})
		if err != nil {
			return codersdk.Response{}, xerrors.Errorf("copy path: %w", err)
		}
		return codersdk.Response{Message: "Path copied."}, nil
	},
}

type WorkspaceRemovePathArgs struct {
	Workspace    string `json:"workspace"`
	Path         string `json:"path"`
	Host         string `json:"host,omitempty"`
	IdentityFile string `json:"identity_file,omitempty"`
	Recursive    bool   `json:"recursive,omitempty"`
}

var WorkspaceRemovePath = Tool[WorkspaceRemovePathArgs, codersdk.Response]{
	Tool: aisdk.Tool{
		Name:        ToolNameWorkspaceRemovePath,
		Description: `Remove a file, symlink, or directory from the workspace or an SSH target. Directories must be empty unless recursive=true.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"path":      map[string]any{"type": "string", "description": "Absolute path to remove."},
				"host":      map[string]any{"type": "string", "description": "Optional SSH alias returned by remote_hosts. Omit for the workspace filesystem."},
				"recursive": map[string]any{"type": "boolean", "description": "Recursively remove a directory tree. Defaults to false."},
			},
			Required: []string{"workspace", "path"},
		},
	},
	MCPAnnotations:     mcpDestructiveOpenWorldAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceRemovePathArgs) (codersdk.Response, error) {
		endpoint := WorkspacePathEndpoint{Path: args.Path, Host: args.Host, IdentityFile: args.IdentityFile}
		if err := validatePathEndpoint(endpoint); err != nil {
			return codersdk.Response{}, err
		}
		cleaned := path.Clean(args.Path)
		if cleaned == "/" {
			return codersdk.Response{}, xerrors.New("refusing to remove filesystem root")
		}
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return codersdk.Response{}, err
		}
		defer conn.Close()

		command := "if [ ! -e " + toolShellQuote(cleaned) + " ] && [ ! -L " + toolShellQuote(cleaned) + " ]; then echo 'path does not exist' >&2; exit 2; fi; "
		if args.Recursive {
			command += "rm -rf -- " + toolShellQuote(cleaned)
		} else {
			command += "if [ -d " + toolShellQuote(cleaned) + " ] && [ ! -L " + toolShellQuote(cleaned) + " ]; then rmdir -- " + toolShellQuote(cleaned) + "; else rm -- " + toolShellQuote(cleaned) + "; fi"
		}
		_, err = runHelperCommand(ctx, conn, workspacesdk.RunCommandRequest{Host: args.Host, IdentityFile: args.IdentityFile, Command: command})
		if err != nil {
			return codersdk.Response{}, xerrors.Errorf("remove path: %w", err)
		}
		return codersdk.Response{Message: "Path removed."}, nil
	},
}
