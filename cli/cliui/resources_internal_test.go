package cliui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderAgentVersion(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name          string
		agentVersion  string
		serverVersion string
		expected      string
	}{
		{
			name:          "OK",
			agentVersion:  "v1.2.3",
			serverVersion: "v1.2.3",
			expected:      "v1.2.3",
		},
		{
			name:          "Outdated",
			agentVersion:  "v1.2.3",
			serverVersion: "v1.2.4",
			expected:      "v1.2.3 (outdated)",
		},
		{
			name:          "CustomReleaseOutdated",
			agentVersion:  "v2.35.3.2+aaaaaaa",
			serverVersion: "v2.35.3.3+bbbbbbb",
			expected:      "v2.35.3.2+aaaaaaa (outdated)",
		},
		{
			name:          "CustomReleaseCurrent",
			agentVersion:  "v2.35.3.3+aaaaaaa",
			serverVersion: "v2.35.3.3+bbbbbbb",
			expected:      "v2.35.3.3+aaaaaaa",
		},
		{
			name:          "AgentUnknown",
			agentVersion:  "",
			serverVersion: "v1.2.4",
			expected:      "(unknown)",
		},
		{
			name:          "ServerUnknown",
			agentVersion:  "v1.2.3",
			serverVersion: "",
			expected:      "v1.2.3",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			actual := renderAgentVersion(testCase.agentVersion, testCase.serverVersion)
			assert.Equal(t, testCase.expected, (actual))
		})
	}
}
