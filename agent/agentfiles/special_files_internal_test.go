package agentfiles

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3"
	"cdr.dev/slog/v3/sloggers/slogtest"
)

type specialFileInfo struct {
	name string
	mode os.FileMode
}

func (i specialFileInfo) Name() string      { return i.name }
func (specialFileInfo) Size() int64         { return 0 }
func (i specialFileInfo) Mode() os.FileMode { return i.mode }
func (specialFileInfo) ModTime() time.Time  { return time.Time{} }
func (specialFileInfo) IsDir() bool         { return false }
func (specialFileInfo) Sys() any            { return nil }

type specialStatFS struct {
	afero.Fs
	specialPath string
	specialMode os.FileMode
	openCalled  bool
}

func (fs *specialStatFS) Stat(name string) (os.FileInfo, error) {
	if name == fs.specialPath {
		return specialFileInfo{name: "special", mode: fs.specialMode | 0o600}, nil
	}
	return fs.Fs.Stat(name)
}

func (fs *specialStatFS) Open(name string) (afero.File, error) {
	if name == fs.specialPath {
		fs.openCalled = true
		panic("special file must be rejected before Open")
	}
	return fs.Fs.Open(name)
}

func TestStreamFileRejectsSpecialFileBeforeOpen(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		mode os.FileMode
	}{
		{name: "named-pipe", mode: os.ModeNamedPipe},
		{name: "socket", mode: os.ModeSocket},
		{name: "device", mode: os.ModeDevice},
		{name: "char-device", mode: os.ModeDevice | os.ModeCharDevice},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			const path = "/tmp/fake-special"
			fs := &specialStatFS{
				Fs:          afero.NewMemMapFs(),
				specialPath: path,
				specialMode: tc.mode,
			}
			logger := slogtest.Make(t, &slogtest.Options{IgnoreErrors: true}).Leveled(slog.LevelDebug)
			api := NewAPI(logger, fs, nil)
			recorder := httptest.NewRecorder()

			status, err := api.streamFile(t.Context(), recorder, path, 0, 0)
			require.Error(t, err)
			require.Equal(t, http.StatusBadRequest, status)
			require.Contains(t, err.Error(), "not a regular file")
			require.False(t, fs.openCalled)
		})
	}
}
