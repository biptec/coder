package toolsdk

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommandActivityCorrelation(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		CommandActivityCorrelation("echo hello", nil),
		CommandActivityCorrelation("echo hello", []string{}),
		"nil and empty argv must normalize identically",
	)
	require.NotEqual(
		t,
		CommandActivityCorrelation("echo hello", []string{}),
		CommandActivityCorrelation("echo goodbye", []string{}),
	)
	require.NotEqual(
		t,
		CommandActivityCorrelation("", []string{"echo", "hello"}),
		CommandActivityCorrelation("echo hello", []string{}),
		"argv execution and shell command execution are different shapes",
	)
	longCommand := strings.Repeat("x", 16*1024)
	require.Len(t, CommandActivityCorrelation(longCommand, nil), 64)
}
