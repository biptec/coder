package toolsdk

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

const (
	sudoEphemeralFilesystemAdvisoryCode = "SUDO_EPHEMERAL_ROOTFS"
	sudoEphemeralFilesystemAdvisoryText = "This workspace's system filesystem is ephemeral. In the standard Developer Workspace, only /home/coder is persistent across workspace recreation. Treat changes made with sudo outside /home/coder as temporary; they will be lost when the workspace is recreated. Prefer durable tools and dependencies under $HOME, and use sudo for system changes only when they are intentionally temporary."
)

// ToolAdvisory is structured guidance returned alongside tool output without
// modifying stdout or stderr from the command itself.
type ToolAdvisory struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func commandAdvisories(command string) []ToolAdvisory {
	if !commandInvokesSudo(command) {
		return nil
	}
	return []ToolAdvisory{{
		Code:    sudoEphemeralFilesystemAdvisoryCode,
		Message: sudoEphemeralFilesystemAdvisoryText,
	}}
}

// commandInvokesSudo reports whether command contains a literal invocation of
// sudo anywhere in the shell syntax tree. Parsing the shell AST avoids false
// positives for comments, quoted data, and arguments that merely contain the
// word "sudo", while still finding sudo in pipelines, compound commands,
// subshells, and command substitutions.
func commandInvokesSudo(command string) bool {
	file, _ := syntax.NewParser().Parse(strings.NewReader(command), "")
	if file == nil {
		return false
	}

	found := false
	syntax.Walk(file, func(node syntax.Node) bool {
		if found {
			return false
		}

		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}

		program := literalShellWord(call.Args[0])
		if program == "" {
			return true
		}
		if i := strings.LastIndexAny(program, `/\`); i >= 0 {
			program = program[i+1:]
		}
		if program == "sudo" {
			found = true
			return false
		}
		return true
	})
	return found
}

func literalShellWord(word *syntax.Word) string {
	if word == nil {
		return ""
	}

	var out strings.Builder
	for _, part := range word.Parts {
		switch part := part.(type) {
		case *syntax.Lit:
			_, _ = out.WriteString(part.Value)
		case *syntax.SglQuoted:
			_, _ = out.WriteString(part.Value)
		case *syntax.DblQuoted:
			for _, inner := range part.Parts {
				lit, ok := inner.(*syntax.Lit)
				if !ok {
					return ""
				}
				_, _ = out.WriteString(lit.Value)
			}
		default:
			return ""
		}
	}
	return out.String()
}
