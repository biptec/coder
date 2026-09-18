package dbauthz_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNoUnimplementedDatabaseMethods(t *testing.T) {
	t.Parallel()

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)

	source, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "dbauthz.go"))
	require.NoError(t, err)
	require.NotContains(t, string(source), `panic("not implemented")`,
		"database generation added an authorization stub; implement its dbauthz policy before merging")
}
