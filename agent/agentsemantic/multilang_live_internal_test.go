package agentsemantic

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/testutil"
)

func useLiveBackend(t *testing.T, backend *semanticBackend, overrideEnv string) {
	t.Helper()

	original := *backend
	if override := os.Getenv(overrideEnv); override != "" {
		info, err := os.Stat(override)
		require.NoError(t, err)
		require.True(t, info.Mode().IsRegular())
		require.NotZero(t, info.Mode().Perm()&0o111)
		backend.executable = override
		t.Cleanup(func() {
			*backend = original
		})
	}
	if _, err := os.Stat(backend.executable); err != nil {
		t.Skipf("%s semantic backend is not installed in this test environment: %v", backend.displayName, err)
	}
	if os.Getenv(overrideEnv) == "" {
		if err := exec.Command(backend.executable, "--version").Run(); err != nil {
			t.Skipf("%s semantic backend is not usable in this test environment: %v", backend.displayName, err)
		}
	}
}

func findLiveSymbol(t *testing.T, manager *Manager, ctx context.Context, path, name string) workspacesdk.SemanticSymbol {
	t.Helper()
	result, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: path, Query: name, Match: "exact", Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, result.Symbols, 1)
	return result.Symbols[0]
}

//nolint:paralleltest // Mutates the backend executable only when a manual override is supplied.
func TestLiveTypeScriptSemanticBackend(t *testing.T) {
	useLiveBackend(t, &typeScriptBackend, "CODER_TEST_TYPESCRIPT_LANGUAGE_SERVER")
	if override := os.Getenv("CODER_TEST_TSSERVER"); override != "" {
		originalFiles := append([]string(nil), typeScriptBackend.requiredFiles...)
		originalOptions := typeScriptBackend.initializationOptions
		typeScriptBackend.requiredFiles = []string{override}
		typeScriptBackend.initializationOptions = map[string]any{
			"tsserver": map[string]any{"path": override},
		}
		t.Cleanup(func() {
			typeScriptBackend.requiredFiles = originalFiles
			typeScriptBackend.initializationOptions = originalOptions
		})
	}

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte("{\"compilerOptions\":{\"strict\":true,\"target\":\"ES2022\",\"allowJs\":true,\"checkJs\":true}}\n"), 0o600))
	path := filepath.Join(root, "main.ts")
	require.NoError(t, os.WriteFile(path, []byte("export interface Greeter {\n\tgreet(): string;\n}\n\nexport class English implements Greeter {\n\tgreet(): string {\n\t\treturn \"hello\";\n\t}\n}\n\nexport function target(value: number): number {\n\treturn value + 1;\n}\n\nexport function caller(): number {\n\treturn target(41);\n}\n"), 0o600))
	brokenPath := filepath.Join(root, "broken.ts")
	require.NoError(t, os.WriteFile(brokenPath, []byte("export const broken: number = \"wrong\";\n"), 0o600))
	jsPath := filepath.Join(root, "helper.js")
	require.NoError(t, os.WriteFile(jsPath, []byte("export function jsTarget(value) { return value + 1; }\nexport function jsCaller() { return jsTarget(41); }\n"), 0o600))

	manager := newLiveSemanticManager(t)
	ctx := testutil.Context(t, testutil.WaitLong)

	directorySymbols, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: root, Query: "target", Match: "exact", Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, directorySymbols.Symbols, 1)
	require.Equal(t, languageTypeScript, directorySymbols.Symbols[0].Language)
	require.Equal(t, path, directorySymbols.Symbols[0].Path)

	target := findLiveSymbol(t, manager, ctx, path, "target")
	require.Equal(t, languageTypeScript, target.Language)

	references, err := manager.FindReferences(ctx, workspacesdk.SemanticFindReferencesRequest{
		Target: target.Locator, Limit: 20,
	})
	require.NoError(t, err)
	require.NotEmpty(t, references.References)
	for _, reference := range references.References {
		require.Equal(t, languageTypeScript, reference.Language)
	}

	jsTarget := findLiveSymbol(t, manager, ctx, jsPath, "jsTarget")
	require.Equal(t, languageJavaScript, jsTarget.Language)
	jsReferences, err := manager.FindReferences(ctx, workspacesdk.SemanticFindReferencesRequest{
		Target: jsTarget.Locator, Limit: 20,
	})
	require.NoError(t, err)
	require.NotEmpty(t, jsReferences.References)
	for _, reference := range jsReferences.References {
		require.Equal(t, languageJavaScript, reference.Language)
	}
	manager.mu.Lock()
	require.Len(t, manager.sessions, 1, "TypeScript and JavaScript in one project must reuse one language-server session")
	manager.mu.Unlock()

	greeter := findLiveSymbol(t, manager, ctx, path, "Greeter")
	implementations, err := manager.FindImplementations(ctx, workspacesdk.SemanticFindImplementationsRequest{
		Target: greeter.Locator, Limit: 20,
	})
	require.NoError(t, err)
	require.NotEmpty(t, implementations.Implementations)
	for _, implementation := range implementations.Implementations {
		require.Equal(t, languageTypeScript, implementation.Language)
	}

	diagnostics, err := manager.GetDiagnostics(ctx, workspacesdk.SemanticDiagnosticsRequest{
		Paths: []string{brokenPath}, MinimumSeverity: "error", Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, diagnostics.Files, 1)
	require.Equal(t, languageTypeScript, diagnostics.Files[0].Language)
	require.Equal(t, statusOK, diagnostics.Files[0].Status)
	require.NotEmpty(t, diagnostics.Diagnostics)
}

//nolint:paralleltest // Mutates the backend executable only when a manual override is supplied.
func TestLivePythonSemanticBackend(t *testing.T) {
	useLiveBackend(t, &pythonBackend, "CODER_TEST_BASEDPYRIGHT_LANGSERVER")

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[tool.basedpyright]\ntypeCheckingMode = \"strict\"\n"), 0o600))
	path := filepath.Join(root, "main.py")
	require.NoError(t, os.WriteFile(path, []byte("class Base:\n    def run(self) -> int:\n        raise NotImplementedError\n\nclass Impl(Base):\n    def run(self) -> int:\n        return 1\n\ndef target(value: int) -> int:\n    return value + 1\n\ndef caller() -> int:\n    return target(41)\n"), 0o600))
	brokenPath := filepath.Join(root, "broken.py")
	require.NoError(t, os.WriteFile(brokenPath, []byte("broken: int = \"wrong\"\n"), 0o600))

	manager := newLiveSemanticManager(t)
	ctx := testutil.Context(t, testutil.WaitLong)

	directorySymbols, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: root, Query: "target", Match: "exact", Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, directorySymbols.Symbols, 1)
	require.Equal(t, languagePython, directorySymbols.Symbols[0].Language)
	require.Equal(t, path, directorySymbols.Symbols[0].Path)

	target := findLiveSymbol(t, manager, ctx, path, "target")
	require.Equal(t, languagePython, target.Language)

	references, err := manager.FindReferences(ctx, workspacesdk.SemanticFindReferencesRequest{
		Target: target.Locator, Limit: 20,
	})
	require.NoError(t, err)
	require.NotEmpty(t, references.References)
	for _, reference := range references.References {
		require.Equal(t, languagePython, reference.Language)
	}

	base := findLiveSymbol(t, manager, ctx, path, "Base")
	implementations, err := manager.FindImplementations(ctx, workspacesdk.SemanticFindImplementationsRequest{
		Target: base.Locator, Limit: 20,
	})
	require.NoError(t, err)
	require.NotEmpty(t, implementations.Implementations)
	for _, implementation := range implementations.Implementations {
		require.Equal(t, languagePython, implementation.Language)
	}

	diagnostics, err := manager.GetDiagnostics(ctx, workspacesdk.SemanticDiagnosticsRequest{
		Paths: []string{brokenPath}, MinimumSeverity: "error", Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, diagnostics.Files, 1)
	require.Equal(t, languagePython, diagnostics.Files[0].Language)
	require.Equal(t, statusOK, diagnostics.Files[0].Status)
	require.NotEmpty(t, diagnostics.Diagnostics)
}

//nolint:paralleltest // Mutates the backend executable only when a manual override is supplied.
func TestLiveRustStandaloneSemanticBackend(t *testing.T) {
	useLiveBackend(t, &rustBackend, "CODER_TEST_RUST_ANALYZER")

	root := t.TempDir()
	path := filepath.Join(root, "standalone.rs")
	require.NoError(t, os.WriteFile(path, []byte("pub fn target() {}\npub fn caller() { target(); }\n"), 0o600))

	manager := newLiveSemanticManager(t)
	ctx := testutil.Context(t, testutil.WaitLong)

	target := findLiveSymbol(t, manager, ctx, path, "target")
	_, err := manager.FindReferences(ctx, workspacesdk.SemanticFindReferencesRequest{
		Target: target.Locator, Limit: 20,
	})
	require.NoError(t, err)
}

func TestLiveRustSemanticBackend(t *testing.T) {
	useLiveBackend(t, &rustBackend, "CODER_TEST_RUST_ANALYZER")

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[package]\nname = \"semantic-fixture\"\nversion = \"0.1.0\"\nedition = \"2024\"\n"), 0o600))
	src := filepath.Join(root, "src")
	require.NoError(t, os.MkdirAll(src, 0o755))
	path := filepath.Join(src, "lib.rs")
	require.NoError(t, os.WriteFile(path, []byte("pub trait Greeter {\n    fn greet(&self) -> &'static str;\n}\n\npub struct English;\n\nimpl Greeter for English {\n    fn greet(&self) -> &'static str {\n        \"hello\"\n    }\n}\n\npub fn target(value: i32) -> i32 {\n    value + 1\n}\n\npub fn caller() -> i32 {\n    target(41)\n}\n"), 0o600))

	manager := newLiveSemanticManager(t)
	ctx := testutil.Context(t, testutil.WaitLong)

	directorySymbols, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: root, Query: "target", Match: "exact", Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, directorySymbols.Symbols, 1)
	require.Equal(t, languageRust, directorySymbols.Symbols[0].Language)
	require.Equal(t, path, directorySymbols.Symbols[0].Path)

	target := findLiveSymbol(t, manager, ctx, path, "target")
	require.Equal(t, languageRust, target.Language)

	references, err := manager.FindReferences(ctx, workspacesdk.SemanticFindReferencesRequest{
		Target: target.Locator, Limit: 20,
	})
	require.NoError(t, err)
	require.NotEmpty(t, references.References)
	for _, reference := range references.References {
		require.Equal(t, languageRust, reference.Language)
	}

	methods, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: path, Query: "greet", Match: "exact", Limit: 20,
	})
	require.NoError(t, err)
	require.NotEmpty(t, methods.Symbols)
	var traitMethod *workspacesdk.SemanticSymbol
	for i := range methods.Symbols {
		if methods.Symbols[i].SelectionRange.Start.Line == 2 {
			traitMethod = &methods.Symbols[i]
			break
		}
	}
	require.NotNil(t, traitMethod, "Rust trait method must be discoverable semantically")
	implementations, err := manager.FindImplementations(ctx, workspacesdk.SemanticFindImplementationsRequest{
		Target: traitMethod.Locator, Limit: 20,
	})
	require.NoError(t, err)
	require.NotEmpty(t, implementations.Implementations)
	for _, implementation := range implementations.Implementations {
		require.Equal(t, languageRust, implementation.Language)
	}

	diagnostics, err := manager.GetDiagnostics(ctx, workspacesdk.SemanticDiagnosticsRequest{
		Paths: []string{path}, MinimumSeverity: "hint", Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, diagnostics.Files, 1)
	require.Equal(t, languageRust, diagnostics.Files[0].Language)
	require.Equal(t, statusOK, diagnostics.Files[0].Status)
}
