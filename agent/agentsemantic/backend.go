package agentsemantic

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const (
	trustedGoplsPath                    = "/usr/local/bin/gopls"
	trustedTypeScriptLanguageServerPath = "/usr/local/bin/typescript-language-server"
	trustedTypeScriptServerPath         = "/usr/local/lib/developer-workspace/typescript/tsserver.js"
	trustedBasedPyrightPath             = "/usr/local/bin/basedpyright-langserver"
	trustedRustAnalyzerPath             = "/usr/local/bin/rust-analyzer"

	languageGo         = "go"
	languageTypeScript = "typescript"
	languageJavaScript = "javascript"
	languagePython     = "python"
	languageRust       = "rust"
)

type semanticBackend struct {
	id                     string
	displayName            string
	executable             string
	args                   []string
	requiredFiles          []string
	initializationOptions  map[string]any
	readinessProgressToken string
	readinessMarkers       []string
}

var (
	goBackend = semanticBackend{
		id:          languageGo,
		displayName: "Go",
		executable:  trustedGoplsPath,
	}
	typeScriptBackend = semanticBackend{
		id:            "typescript-javascript",
		displayName:   "TypeScript/JavaScript",
		executable:    trustedTypeScriptLanguageServerPath,
		args:          []string{"--stdio"},
		requiredFiles: []string{trustedTypeScriptServerPath},
		initializationOptions: map[string]any{
			"tsserver": map[string]any{"path": trustedTypeScriptServerPath},
		},
	}
	pythonBackend = semanticBackend{
		id:          languagePython,
		displayName: "Python",
		executable:  trustedBasedPyrightPath,
		args:        []string{"--stdio"},
	}
	rustBackend = semanticBackend{
		id:                     languageRust,
		displayName:            "Rust",
		executable:             trustedRustAnalyzerPath,
		readinessProgressToken: "rustAnalyzer/cachePriming",
		readinessMarkers:       []string{"Cargo.toml", "rust-project.json"},
	}
)

type semanticProjectRoot struct {
	backend semanticBackend
	root    string
}

func semanticBackendForPath(path string) (semanticBackend, string, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return goBackend, languageGo, true
	case ".ts", ".tsx", ".mts", ".cts":
		return typeScriptBackend, languageTypeScript, true
	case ".js", ".jsx", ".mjs", ".cjs":
		return typeScriptBackend, languageJavaScript, true
	case ".py", ".pyi":
		return pythonBackend, languagePython, true
	case ".rs":
		return rustBackend, languageRust, true
	default:
		return semanticBackend{}, "", false
	}
}

func lspLanguageIDForPath(path string) (string, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go", true
	case ".ts", ".mts", ".cts":
		return "typescript", true
	case ".tsx":
		return "typescriptreact", true
	case ".js", ".mjs", ".cjs":
		return "javascript", true
	case ".jsx":
		return "javascriptreact", true
	case ".py", ".pyi":
		return "python", true
	case ".rs":
		return "rust", true
	default:
		return "", false
	}
}

func validateSemanticFile(path string) (string, semanticBackend, string, error) {
	canonical, err := canonicalExistingPath(path)
	if err != nil {
		return "", semanticBackend{}, "", semanticError(CodeInvalidPath, "Semantic file path is invalid.", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", semanticBackend{}, "", semanticError(CodeInvalidPath, "Semantic file path is invalid.", err)
	}
	if !info.Mode().IsRegular() {
		return "", semanticBackend{}, "", semanticError(
			CodeInvalidPath,
			"Semantic file path must be a regular file.",
			xerrors.Errorf("%q is not a regular file", canonical),
		)
	}
	backend, language, ok := semanticBackendForPath(canonical)
	if !ok {
		return "", semanticBackend{}, "", semanticError(
			CodeUnsupportedLanguage,
			"No semantic backend is available for "+detectedLanguage(canonical)+" in this workspace.",
			nil,
		)
	}
	return canonical, backend, language, nil
}

func semanticProjectRootForFile(path string, backend semanticBackend) (string, error) {
	if backend.id == goBackend.id {
		return goProjectRootForFile(path)
	}
	path, err := canonicalExistingPath(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	dir := path
	if !info.IsDir() {
		dir = filepath.Dir(path)
	}
	var markers []string
	switch backend.id {
	case typeScriptBackend.id:
		markers = []string{"tsconfig.json", "jsconfig.json", "package.json"}
	case pythonBackend.id:
		markers = []string{"pyrightconfig.json", "pyproject.toml", "setup.cfg", "setup.py"}
	case rustBackend.id:
		markers = []string{"Cargo.toml", "rust-project.json"}
	default:
		return "", xerrors.Errorf("unknown semantic backend %q", backend.id)
	}
	if marker := findUpwardAny(dir, markers); marker != "" {
		return filepath.Dir(marker), nil
	}
	return dir, nil
}

func findUpwardAny(startDir string, names []string) string {
	dir := filepath.Clean(startDir)
	for {
		for _, name := range names {
			candidate := filepath.Join(dir, name)
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
				return candidate
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func semanticProjectRootsForDirectory(root string) ([]semanticProjectRoot, workspacesdk.SemanticCoverage, error) {
	root, err := canonicalExistingPath(root)
	if err != nil {
		return nil, workspacesdk.SemanticCoverage{}, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, workspacesdk.SemanticCoverage{}, err
	}
	if !info.IsDir() {
		backend, _, ok := semanticBackendForPath(root)
		if !ok {
			return nil, workspacesdk.SemanticCoverage{}, nil
		}
		projectRoot, rootErr := semanticProjectRootForFile(root, backend)
		if rootErr != nil {
			return nil, workspacesdk.SemanticCoverage{}, rootErr
		}
		return []semanticProjectRoot{{backend: backend, root: projectRoot}}, workspacesdk.SemanticCoverage{Status: coverageComplete}, nil
	}

	roots := make(map[string]semanticProjectRoot)
	dirRootCache := make(map[string]string)
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && shouldSkipSemanticDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		backend, _, ok := semanticBackendForPath(path)
		if !ok {
			return nil
		}
		cacheKey := backend.id + ":" + filepath.Dir(path)
		projectRoot, cached := dirRootCache[cacheKey]
		if !cached {
			var rootErr error
			projectRoot, rootErr = semanticProjectRootForFile(path, backend)
			if rootErr != nil {
				return rootErr
			}
			dirRootCache[cacheKey] = projectRoot
		}
		roots[backend.id+":"+projectRoot] = semanticProjectRoot{backend: backend, root: projectRoot}
		return nil
	})
	if err != nil {
		return nil, workspacesdk.SemanticCoverage{}, err
	}

	result := make([]semanticProjectRoot, 0, len(roots))
	for _, projectRoot := range roots {
		result = append(result, projectRoot)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].backend.id != result[j].backend.id {
			return result[i].backend.id < result[j].backend.id
		}
		return result[i].root < result[j].root
	})
	if len(result) == 0 {
		return result, workspacesdk.SemanticCoverage{Status: coverageComplete}, nil
	}
	return result, workspacesdk.SemanticCoverage{
		Status: coverageUnknown,
		Reason: "Workspace symbol completeness depends on each semantic backend index state.",
	}, nil
}

func readinessProgressTokenForRoot(backend semanticBackend, root string) string {
	if backend.readinessProgressToken == "" {
		return ""
	}
	if len(backend.readinessMarkers) == 0 {
		return backend.readinessProgressToken
	}
	for _, marker := range backend.readinessMarkers {
		info, err := os.Stat(filepath.Join(root, marker))
		if err == nil && info.Mode().IsRegular() {
			return backend.readinessProgressToken
		}
	}
	return ""
}

func shouldSkipSemanticDirectory(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "node_modules", "vendor", "target", ".venv", "venv", "__pycache__", ".mypy_cache", ".pytest_cache":
		return true
	default:
		return false
	}
}

func semanticLanguageForLocation(path string, backend semanticBackend) (string, bool) {
	locationBackend, language, ok := semanticBackendForPath(path)
	if !ok || locationBackend.id != backend.id {
		return "", false
	}
	return language, true
}
