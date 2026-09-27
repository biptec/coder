package toolsdk

import (
	"strings"
	"testing"
)

func TestToolSelectionGuidance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		description string
		contains    []string
		excludes    []string
	}{
		{
			name:        "find_symbol",
			description: WorkspaceFindSymbol.Description,
			contains: []string{
				"default way to locate language-level symbols",
				"Prefer it over",
				"start_search",
				"grep",
				"fall back to",
			},
		},
		{
			name:        "find_references",
			description: WorkspaceFindReferences.Description,
			contains: []string{
				"default tool for usages, call sites, and impact analysis",
				"Prefer it over grep",
				"explicit fallback",
			},
		},
		{
			name:        "find_implementations",
			description: WorkspaceFindImplementations.Description,
			contains: []string{
				"default tool for interface/trait implementations",
				"Prefer it over searching",
				"capability_unsupported",
			},
		},
		{
			name:        "get_diagnostics",
			description: WorkspaceGetDiagnostics.Description,
			contains: []string{
				"after editing supported source files",
				"invoking a language server manually",
				"authoritative for whole-project validation",
			},
		},
		{
			name:        "read_file",
			description: WorkspaceReadFileV2.Description,
			contains: []string{
				"known workspace file directly",
				"prefer this over shell cat",
				"prefer semantic tools first",
			},
		},
		{
			name:        "read_multiple_files",
			description: WorkspaceReadFilesV2.Description,
			contains: []string{
				"several known workspace files",
				"prefer this over repeated reads or shell loops",
			},
		},
		{
			name:        "edit_file",
			description: WorkspaceEditFile.Description,
			contains: []string{
				"prefer this over sed/Python/shell editing",
				"sed",
				"shell redirection",
			},
		},
		{
			name:        "edit_multiple_files",
			description: WorkspaceEditFiles.Description,
			contains: []string{
				"Coordinate validated edits",
				"validated before writes begin",
				"do not assume rollback/transaction semantics",
			},
		},
		{
			name:        "start_search",
			description: WorkspaceSearchStart.Description,
			contains: []string{
				"literal/regex text search",
				"prefer",
				"find_symbol/find_references/find_implementations",
			},
		},
		{
			name:        "start_process",
			description: WorkspaceProcessStartV2.Description,
			contains: []string{
				"builds, tests, Git, formatters",
				"Do not use process commands as a substitute",
				"Do not start or manage language servers manually",
			},
			excludes: []string{
				"start language servers",
			},
		},
		{
			name:        "execute_shell_command",
			description: WorkspaceBash.Description,
			contains: []string{
				"only when shell syntax",
				"Do not use shell cat/grep/sed/awk commands",
				"dedicated semantic navigation or file read/edit tools",
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			firstLine := strings.SplitN(tt.description, "\n", 2)[0]
			if !strings.Contains(strings.ToLower(firstLine), "prefer") && tt.name != "get_diagnostics" && tt.name != "start_process" && tt.name != "execute_shell_command" {
				t.Fatalf("first description line must guide compact list_tools selection: %q", firstLine)
			}
			if tt.name == "get_diagnostics" && !strings.Contains(firstLine, "build/test") {
				t.Fatalf("diagnostics first line must distinguish file diagnostics from project validation: %q", firstLine)
			}
			if tt.name == "start_process" && !strings.Contains(firstLine, "use semantic/file tools") {
				t.Fatalf("start_process first line must distinguish execution from code/file navigation: %q", firstLine)
			}
			if tt.name == "execute_shell_command" && !strings.Contains(firstLine, "do not substitute it for semantic/file tools") {
				t.Fatalf("execute_shell_command first line must keep shell use narrow: %q", firstLine)
			}
			for _, want := range tt.contains {
				if !strings.Contains(tt.description, want) {
					t.Fatalf("description missing %q:\n%s", want, tt.description)
				}
			}
			for _, unwanted := range tt.excludes {
				if strings.Contains(tt.description, unwanted) {
					t.Fatalf("description unexpectedly contains %q:\n%s", unwanted, tt.description)
				}
			}
		})
	}
}
