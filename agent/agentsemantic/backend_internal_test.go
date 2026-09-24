package agentsemantic

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSemanticBackendForPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		path       string
		backendID  string
		language   string
		languageID string
	}{
		{name: "go", path: "main.go", backendID: "go", language: "go", languageID: "go"},
		{name: "typescript", path: "main.ts", backendID: "typescript-javascript", language: "typescript", languageID: "typescript"},
		{name: "typescript module", path: "main.mts", backendID: "typescript-javascript", language: "typescript", languageID: "typescript"},
		{name: "typescript commonjs", path: "main.cts", backendID: "typescript-javascript", language: "typescript", languageID: "typescript"},
		{name: "typescript react", path: "view.tsx", backendID: "typescript-javascript", language: "typescript", languageID: "typescriptreact"},
		{name: "javascript", path: "main.js", backendID: "typescript-javascript", language: "javascript", languageID: "javascript"},
		{name: "javascript react", path: "view.jsx", backendID: "typescript-javascript", language: "javascript", languageID: "javascriptreact"},
		{name: "javascript module", path: "main.mjs", backendID: "typescript-javascript", language: "javascript", languageID: "javascript"},
		{name: "javascript commonjs", path: "main.cjs", backendID: "typescript-javascript", language: "javascript", languageID: "javascript"},
		{name: "python", path: "main.py", backendID: "python", language: "python", languageID: "python"},
		{name: "python stub", path: "types.pyi", backendID: "python", language: "python", languageID: "python"},
		{name: "rust", path: "lib.rs", backendID: "rust", language: "rust", languageID: "rust"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			backend, language, ok := semanticBackendForPath(test.path)
			require.True(t, ok)
			require.Equal(t, test.backendID, backend.id)
			require.Equal(t, test.language, language)

			languageID, ok := lspLanguageIDForPath(test.path)
			require.True(t, ok)
			require.Equal(t, test.languageID, languageID)
		})
	}

	_, _, ok := semanticBackendForPath("Main.java")
	require.False(t, ok)
	_, ok = lspLanguageIDForPath("README.md")
	require.False(t, ok)
}

func TestSemanticBackendTrustedExecutables(t *testing.T) {
	t.Parallel()

	require.Equal(t, "/usr/local/bin/gopls", goBackend.executable)
	require.Equal(t, "/usr/local/bin/typescript-language-server", typeScriptBackend.executable)
	require.Equal(t, []string{"--stdio"}, typeScriptBackend.args)
	require.Equal(t, []string{"/usr/local/lib/developer-workspace/typescript/tsserver.js"}, typeScriptBackend.requiredFiles)
	require.Equal(t, "/usr/local/lib/developer-workspace/typescript/tsserver.js", typeScriptBackend.initializationOptions["tsserver"].(map[string]any)["path"])
	require.Equal(t, "/usr/local/bin/basedpyright-langserver", pythonBackend.executable)
	require.Equal(t, []string{"--stdio"}, pythonBackend.args)
	require.Equal(t, "/usr/local/bin/rust-analyzer", rustBackend.executable)
	require.Empty(t, rustBackend.args)
	require.Equal(t, "rustAnalyzer/cachePriming", rustBackend.readinessProgressToken)
	require.Equal(t, []string{"Cargo.toml", "rust-project.json"}, rustBackend.readinessMarkers)
}

func TestSemanticProjectRootForFile(t *testing.T) {
	t.Parallel()

	t.Run("typescript prefers nearest config marker", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte("{}\n"), 0o600))
		app := filepath.Join(root, "app")
		require.NoError(t, os.MkdirAll(filepath.Join(app, "src"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(app, "tsconfig.json"), []byte("{}\n"), 0o600))
		path := filepath.Join(app, "src", "main.ts")
		require.NoError(t, os.WriteFile(path, []byte("export const value = 1;\n"), 0o600))

		got, err := semanticProjectRootForFile(path, typeScriptBackend)
		require.NoError(t, err)
		require.Equal(t, app, got)
	})

	t.Run("javascript uses package root", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte("{}\n"), 0o600))
		path := filepath.Join(root, "src", "main.js")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("export const value = 1;\n"), 0o600))

		got, err := semanticProjectRootForFile(path, typeScriptBackend)
		require.NoError(t, err)
		require.Equal(t, root, got)
	})

	t.Run("python uses pyproject root", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[project]\nname='fixture'\n"), 0o600))
		path := filepath.Join(root, "pkg", "main.py")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("value = 1\n"), 0o600))

		got, err := semanticProjectRootForFile(path, pythonBackend)
		require.NoError(t, err)
		require.Equal(t, root, got)
	})

	t.Run("rust uses cargo root", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[package]\nname='fixture'\nversion='0.1.0'\n"), 0o600))
		path := filepath.Join(root, "src", "lib.rs")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("pub fn value() -> i32 { 1 }\n"), 0o600))

		got, err := semanticProjectRootForFile(path, rustBackend)
		require.NoError(t, err)
		require.Equal(t, root, got)
	})
}

func TestSemanticProjectRootsForDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	goRoot := filepath.Join(root, "goapp")
	require.NoError(t, os.MkdirAll(goRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(goRoot, "go.mod"), []byte("module example.com/goapp\n\ngo 1.26\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(goRoot, "main.go"), []byte("package goapp\n"), 0o600))

	tsRoot := filepath.Join(root, "web")
	require.NoError(t, os.MkdirAll(filepath.Join(tsRoot, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tsRoot, "tsconfig.json"), []byte("{}\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(tsRoot, "src", "main.ts"), []byte("export const value = 1;\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(tsRoot, "src", "helper.js"), []byte("export const helper = 1;\n"), 0o600))

	pythonRoot := filepath.Join(root, "python")
	require.NoError(t, os.MkdirAll(pythonRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pythonRoot, "pyproject.toml"), []byte("[project]\nname='fixture'\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(pythonRoot, "main.py"), []byte("value = 1\n"), 0o600))

	rustRoot := filepath.Join(root, "rust")
	require.NoError(t, os.MkdirAll(filepath.Join(rustRoot, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(rustRoot, "Cargo.toml"), []byte("[package]\nname='fixture'\nversion='0.1.0'\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(rustRoot, "src", "lib.rs"), []byte("pub fn value() -> i32 { 1 }\n"), 0o600))

	ignored := filepath.Join(root, "node_modules", "ignored")
	require.NoError(t, os.MkdirAll(ignored, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ignored, "index.ts"), []byte("export const ignored = 1;\n"), 0o600))

	roots, coverage, err := semanticProjectRootsForDirectory(root)
	require.NoError(t, err)
	require.Equal(t, coverageUnknown, coverage.Status)

	got := make(map[string]string, len(roots))
	for _, projectRoot := range roots {
		got[projectRoot.backend.id] = projectRoot.root
	}
	require.Equal(t, map[string]string{
		goBackend.id:         goRoot,
		typeScriptBackend.id: tsRoot,
		pythonBackend.id:     pythonRoot,
		rustBackend.id:       rustRoot,
	}, got)
}
