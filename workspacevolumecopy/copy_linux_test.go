//go:build linux

package workspacevolumecopy

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func requireRsync(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/usr/bin/rsync"); err != nil {
		t.Skip("rsync is required")
	}
}

func TestCopyMergeOverwriteAndMetadata(t *testing.T) {
	t.Parallel()
	requireRsync(t)

	source := t.TempDir()
	destination := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(source, "nested"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(source, "new.txt"), []byte("new"), 0o640))
	require.NoError(t, os.WriteFile(filepath.Join(source, "existing.txt"), []byte("source"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(source, "nested", "child.txt"), []byte("child"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(destination, "existing.txt"), []byte("destination"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(destination, "destination-only.txt"), []byte("keep"), 0o600))

	hardlinkSource := filepath.Join(source, "hardlink-source")
	hardlinkPeer := filepath.Join(source, "hardlink-peer")
	require.NoError(t, os.WriteFile(hardlinkSource, []byte("hardlink"), 0o600))
	require.NoError(t, os.Link(hardlinkSource, hardlinkPeer))

	xattrPath := filepath.Join(source, "new.txt")
	xattrSupported := unix.Setxattr(xattrPath, "user.coder-volume-copy", []byte("preserved"), 0) == nil

	err := Copy(context.Background(), Plan{Volumes: []VolumePlan{{
		Key:         "home",
		Source:      source,
		Destination: destination,
		Overwrite:   false,
	}}}, io.Discard, io.Discard)
	require.NoError(t, err)
	requireFileContents(t, filepath.Join(destination, "existing.txt"), "destination")
	requireFileContents(t, filepath.Join(destination, "new.txt"), "new")
	requireFileContents(t, filepath.Join(destination, "nested", "child.txt"), "child")
	requireFileContents(t, filepath.Join(destination, "destination-only.txt"), "keep")

	firstInfo, err := os.Stat(filepath.Join(destination, "hardlink-source"))
	require.NoError(t, err)
	peerInfo, err := os.Stat(filepath.Join(destination, "hardlink-peer"))
	require.NoError(t, err)
	require.True(t, os.SameFile(firstInfo, peerInfo), "hardlink relationship must be preserved")

	if xattrSupported {
		buf := make([]byte, 64)
		n, err := unix.Getxattr(filepath.Join(destination, "new.txt"), "user.coder-volume-copy", buf)
		require.NoError(t, err)
		require.Equal(t, "preserved", string(buf[:n]))
	}

	err = Copy(context.Background(), Plan{Volumes: []VolumePlan{{
		Key:         "home",
		Source:      source,
		Destination: destination,
		Overwrite:   true,
	}}}, io.Discard, io.Discard)
	require.NoError(t, err)
	requireFileContents(t, filepath.Join(destination, "existing.txt"), "source")
	requireFileContents(t, filepath.Join(destination, "destination-only.txt"), "keep")
}

func TestCopyExcludedPathsAndSymlink(t *testing.T) {
	t.Parallel()
	requireRsync(t)

	source := t.TempDir()
	destination := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(source, ".ssh", "config.d"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(source, ".ssh", "id_ed25519_workspace"), []byte("source-key"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(source, ".ssh", "config.d", "managed.conf"), []byte("source"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(destination, ".destination-marker"), []byte("keep"), 0o600))
	require.NoError(t, os.Symlink("target", filepath.Join(source, "link")))

	err := Copy(context.Background(), Plan{Volumes: []VolumePlan{{
		Key:           "home",
		Source:        source,
		Destination:   destination,
		Overwrite:     true,
		ExcludedPaths: []string{".ssh/id_ed25519_workspace", ".ssh/config.d"},
	}}}, io.Discard, io.Discard)
	require.NoError(t, err)
	_, err = os.Lstat(filepath.Join(destination, ".ssh", "id_ed25519_workspace"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Lstat(filepath.Join(destination, ".ssh", "config.d"))
	require.ErrorIs(t, err, os.ErrNotExist)
	target, err := os.Readlink(filepath.Join(destination, "link"))
	require.NoError(t, err)
	require.Equal(t, "target", target)
	requireFileContents(t, filepath.Join(destination, ".destination-marker"), "keep")
}

func requireFileContents(t *testing.T, path, expected string) {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, expected, string(content))
}
