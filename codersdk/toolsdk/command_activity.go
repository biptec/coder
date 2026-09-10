package toolsdk

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// CommandActivityCorrelation returns a deterministic, non-reversible key for
// matching an MCP command request to the Agent command activity it starts.
//
// Only the execution shape participates. Workspace names, environment values,
// stdin, and other potentially sensitive request fields are deliberately
// excluded. nil and empty argv are normalized to the same representation so
// old Agents and newer MCP clients agree on the key.
func CommandActivityCorrelation(command string, argv []string) string {
	if argv == nil {
		argv = []string{}
	}
	payload, err := json.Marshal(struct {
		Command string   `json:"command"`
		Argv    []string `json:"argv"`
	}{
		Command: command,
		Argv:    argv,
	})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
