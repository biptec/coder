package toolsdk

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

func TestWorkspaceEditFileContract(t *testing.T) {
	t.Parallel()

	require.NotContains(t, WorkspaceEditFile.Schema.Properties, "dry_run")
	require.Equal(t, mcpDestructiveAnnotations, WorkspaceEditFile.MCPAnnotations)
	require.Contains(t, WorkspaceEditFile.Description, "unified diff")

	args := WorkspaceEditFileArgs{
		Workspace: "owner/workspace",
		Path:      "/tmp/file.txt",
		Edits: []workspacesdk.FileEdit{{
			Search:  "old",
			Replace: "new",
		}},
	}
	req := workspaceEditFileRequest(args)
	require.False(t, req.DryRun, "dry-run remains an internal Agent capability, not a public tool parameter")
	require.True(t, req.IncludeDiff)
	require.True(t, req.ExactOnly)
	require.Len(t, req.Files, 1)
	require.Equal(t, args.Path, req.Files[0].Path)
	require.Equal(t, args.Edits, req.Files[0].Edits)
}

func TestWorkspaceEditMultipleFilesContract(t *testing.T) {
	t.Parallel()

	require.NotContains(t, WorkspaceEditFiles.Schema.Properties, "dry_run")
	require.Equal(t, mcpDestructiveAnnotations, WorkspaceEditFiles.MCPAnnotations)
	require.Contains(t, WorkspaceEditFiles.Description, "unified diffs")

	files := []workspacesdk.FileEdits{
		{Path: "/tmp/a.txt", Edits: []workspacesdk.FileEdit{{Search: "a", Replace: "A"}}},
		{Path: "/tmp/b.txt", Edits: []workspacesdk.FileEdit{{Search: "b", Replace: "B"}}},
	}
	req := workspaceEditFilesRequest(WorkspaceEditFilesArgs{
		Workspace: "owner/workspace",
		Files:     files,
	})
	require.False(t, req.DryRun)
	require.True(t, req.IncludeDiff)
	require.True(t, req.ExactOnly)
	require.Equal(t, files, req.Files)
}
