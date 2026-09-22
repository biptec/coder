package agentfiles

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3"
	"cdr.dev/slog/v3/sloggers/slogtest"
)

func strictWriteTestLogger(t *testing.T) slog.Logger {
	t.Helper()
	return slogtest.Make(t, &slogtest.Options{IgnoreErrors: true}).Leveled(slog.LevelDebug)
}

func TestWriteFileStrictRequiresExistingParent(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	api := NewAPI(strictWriteTestLogger(t), fs, nil)

	status, err := api.writeFileStrict(context.Background(), "/missing/child.txt", strings.NewReader("payload"), writeFileOptions{})
	require.Error(t, err)
	require.Equal(t, 404, status)
	require.Contains(t, err.Error(), "parent directory does not exist")
	_, statErr := fs.Stat("/missing")
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestWriteFileStrictOverwriteAndPermissions(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "config")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o600))
	require.NoError(t, os.Chmod(path, 0o640))
	api := NewAPI(strictWriteTestLogger(t), afero.NewOsFs(), nil)

	status, err := api.writeFileStrict(context.Background(), path, strings.NewReader("denied"), writeFileOptions{expectedExists: true})
	require.Error(t, err)
	require.Equal(t, 409, status)
	content, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	require.Equal(t, "old", string(content))

	status, err = api.writeFileStrict(context.Background(), path, strings.NewReader("new"), writeFileOptions{overwrite: true, expectedExists: true})
	require.NoError(t, err)
	require.Zero(t, status)
	content, readErr = os.ReadFile(path)
	require.NoError(t, readErr)
	require.Equal(t, "new", string(content))
	info, statErr := os.Stat(path)
	require.NoError(t, statErr)
	require.Equal(t, os.FileMode(0o640), info.Mode().Perm())
}

func TestWriteFileStrictRejectsFinalSymlinkWithoutTouchingTarget(t *testing.T) {
	if testing.Short() || runtime.GOOS == "windows" {
		t.Skip("filesystem symlink test")
	}
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "target")
	link := filepath.Join(root, "link")
	require.NoError(t, os.WriteFile(target, []byte("target-old"), 0o600))
	require.NoError(t, os.Symlink(target, link))
	api := NewAPI(strictWriteTestLogger(t), afero.NewOsFs(), nil)

	status, err := api.writeFileStrict(context.Background(), link, strings.NewReader("danger"), writeFileOptions{overwrite: true, expectedExists: true})
	require.Error(t, err)
	require.Equal(t, 400, status)
	require.Contains(t, err.Error(), "symbolic link")
	require.Contains(t, err.Error(), target)
	content, readErr := os.ReadFile(target)
	require.NoError(t, readErr)
	require.Equal(t, "target-old", string(content))
	info, lstatErr := os.Lstat(link)
	require.NoError(t, lstatErr)
	require.NotZero(t, info.Mode()&os.ModeSymlink)
}

type raceCreateFS struct {
	afero.Fs
	target  string
	created bool
}

func (fs *raceCreateFS) LstatIfPossible(name string) (os.FileInfo, bool, error) {
	lstater, ok := fs.Fs.(afero.Lstater)
	if !ok {
		return nil, false, os.ErrInvalid
	}
	return lstater.LstatIfPossible(name)
}

func (fs *raceCreateFS) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	file, err := fs.Fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	if !fs.created && strings.Contains(filepath.Base(name), ".new.txt.tmp.") {
		fs.created = true
		if writeErr := afero.WriteFile(fs.Fs, fs.target, []byte("racer"), 0o600); writeErr != nil {
			_ = file.Close()
			return nil, writeErr
		}
	}
	return file, nil
}

type noLstatFS struct{ afero.Fs }

type fallbackLstatSymlinkFS struct{ afero.Fs }

func (fs *fallbackLstatSymlinkFS) LstatIfPossible(name string) (os.FileInfo, bool, error) {
	info, err := fs.Fs.Stat(name)
	return info, false, err
}

func (*fallbackLstatSymlinkFS) SymlinkIfPossible(oldname, newname string) error {
	return os.Symlink(oldname, newname)
}

func TestWriteFileStrictFailsClosedWithoutLstat(t *testing.T) {
	t.Parallel()

	base := afero.NewMemMapFs()
	require.NoError(t, base.MkdirAll("/root", 0o755))
	fs := &noLstatFS{Fs: base}
	api := NewAPI(strictWriteTestLogger(t), fs, nil)

	status, err := api.writeFileStrict(context.Background(), "/root/file.txt", strings.NewReader("payload"), writeFileOptions{})
	require.Error(t, err)
	require.Equal(t, 500, status)
	require.Contains(t, err.Error(), "does not support lstat")
	_, statErr := base.Stat("/root/file.txt")
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestWriteFileStrictRejectsSymlinkCapableStatFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation can require elevated privileges on Windows")
	}
	t.Parallel()

	root := t.TempDir()
	fs := &fallbackLstatSymlinkFS{Fs: afero.NewOsFs()}
	api := NewAPI(strictWriteTestLogger(t), fs, nil)

	status, err := api.writeFileStrict(context.Background(), filepath.Join(root, "file.txt"), strings.NewReader("payload"), writeFileOptions{})
	require.Error(t, err)
	require.Equal(t, 500, status)
	require.Contains(t, err.Error(), "did not perform lstat")
}

func TestWriteFileStrictRejectsPreflightExistenceChange(t *testing.T) {
	t.Parallel()

	base := afero.NewMemMapFs()
	require.NoError(t, base.MkdirAll("/root", 0o755))
	require.NoError(t, afero.WriteFile(base, "/root/file.txt", []byte("racer"), 0o600))
	api := NewAPI(strictWriteTestLogger(t), base, nil)

	status, err := api.writeFileStrict(context.Background(), "/root/file.txt", strings.NewReader("assistant"), writeFileOptions{overwrite: true})
	require.Error(t, err)
	require.Equal(t, 409, status)
	require.Contains(t, err.Error(), "changed since preflight")
	content, readErr := afero.ReadFile(base, "/root/file.txt")
	require.NoError(t, readErr)
	require.Equal(t, "racer", string(content))
}

func TestWriteFileStrictFollowsIntermediateSymlinkDeterministically(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation can require elevated privileges on Windows")
	}
	t.Parallel()

	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	linkDir := filepath.Join(root, "link")
	require.NoError(t, os.Mkdir(realDir, 0o755))
	require.NoError(t, os.Symlink(realDir, linkDir))
	path := filepath.Join(linkDir, "file.txt")
	api := NewAPI(strictWriteTestLogger(t), afero.NewOsFs(), nil)

	status, err := api.writeFileStrict(context.Background(), path, strings.NewReader("payload"), writeFileOptions{})
	require.NoError(t, err)
	require.Zero(t, status)
	content, readErr := os.ReadFile(filepath.Join(realDir, "file.txt"))
	require.NoError(t, readErr)
	require.Equal(t, "payload", string(content))
}

func TestWriteFileStrictRechecksOverwriteBeforeCommit(t *testing.T) {
	t.Parallel()

	base := afero.NewMemMapFs()
	require.NoError(t, base.MkdirAll("/root", 0o755))
	fs := &raceCreateFS{Fs: base, target: "/root/new.txt"}
	api := NewAPI(strictWriteTestLogger(t), fs, nil)

	status, err := api.writeFileStrict(context.Background(), "/root/new.txt", strings.NewReader("assistant"), writeFileOptions{overwrite: true})
	require.Error(t, err)
	require.Equal(t, 409, status)
	require.Contains(t, err.Error(), "appeared")
	content, readErr := afero.ReadFile(base, "/root/new.txt")
	require.NoError(t, readErr)
	require.Equal(t, "racer", string(content))

	entries, listErr := afero.ReadDir(base, "/root")
	require.NoError(t, listErr)
	for _, entry := range entries {
		require.NotContains(t, entry.Name(), ".tmp.", "staged temporary file must be removed after race rejection")
	}
}
