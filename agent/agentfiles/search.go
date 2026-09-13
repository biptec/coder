package agentfiles

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/spf13/afero"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/agent/agentchat"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const (
	defaultSearchResults  = 500
	maxSearchResults      = 5000
	maxSearchPage         = 1000
	maxSearchFileBytes    = 2 << 20
	maxSearchLineBytes    = 1 << 20
	maxSearchPreviewRunes = 500
	searchSessionTTL      = 10 * time.Minute
	searchMaxDuration     = 30 * time.Second
)

var errSearchLimit = errors.New("search result limit reached")

type searchSession struct {
	mu      sync.Mutex
	info    workspacesdk.SearchSessionInfo
	results []workspacesdk.SearchResult
	chatID  string
	cancel  context.CancelFunc
}

func (s *searchSession) snapshot() workspacesdk.SearchSessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	info := s.info
	info.ResultCount = len(s.results)
	return info
}

type searchManager struct {
	mu       sync.Mutex
	fs       afero.Fs
	sessions map[string]*searchSession
}

func newSearchManager(fs afero.Fs) *searchManager {
	if fs == nil {
		fs = afero.NewOsFs()
	}
	return &searchManager{fs: fs, sessions: make(map[string]*searchSession)}
}

func (m *searchManager) cleanupLocked(now time.Time) {
	for id, session := range m.sessions {
		info := session.snapshot()
		if info.CompletedAt == nil {
			continue
		}
		if now.Sub(time.Unix(*info.CompletedAt, 0)) > searchSessionTTL {
			delete(m.sessions, id)
		}
	}
}

func searchChatID(ctx context.Context) string {
	if chatContext, ok := agentchat.FromContext(ctx); ok {
		return chatContext.ID.String()
	}
	return ""
}

func (m *searchManager) start(req workspacesdk.SearchStartRequest, chatID string) (string, error) {
	if !filepath.IsAbs(req.Root) {
		return "", xerrors.Errorf("search root must be absolute: %q", req.Root)
	}
	if req.Query == "" {
		return "", xerrors.New("search query must not be empty")
	}
	if req.Mode != "files" && req.Mode != "content" {
		return "", xerrors.New(`search mode must be "files" or "content"`)
	}
	info, err := m.fs.Stat(req.Root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() && req.Mode == "files" {
		return "", xerrors.New("file-name search root must be a directory")
	}
	maxResults := req.MaxResults
	if maxResults == 0 {
		maxResults = defaultSearchResults
	}
	if maxResults < 1 || maxResults > maxSearchResults {
		return "", xerrors.Errorf("max_results must be between 1 and %d", maxSearchResults)
	}
	matcher, err := newSearchMatcher(req.Query, req.Regex, req.CaseSensitive)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), searchMaxDuration)
	id := uuid.New().String()
	session := &searchSession{
		info: workspacesdk.SearchSessionInfo{
			ID:        id,
			Root:      req.Root,
			Query:     req.Query,
			Mode:      req.Mode,
			Status:    "running",
			CreatedAt: time.Now().Unix(),
		},
		chatID: chatID,
		cancel: cancel,
	}

	m.mu.Lock()
	m.cleanupLocked(time.Now())
	m.sessions[id] = session
	m.mu.Unlock()

	go m.run(ctx, session, req, matcher, maxResults)
	return id, nil
}

type searchMatcher struct {
	re            *regexp.Regexp
	literal       string
	caseSensitive bool
}

func newSearchMatcher(query string, regex, caseSensitive bool) (searchMatcher, error) {
	if regex {
		pattern := query
		if !caseSensitive {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return searchMatcher{}, xerrors.Errorf("invalid RE2 search expression: %w", err)
		}
		return searchMatcher{re: re, caseSensitive: caseSensitive}, nil
	}
	literal := query
	if !caseSensitive {
		literal = strings.ToLower(query)
	}
	return searchMatcher{literal: literal, caseSensitive: caseSensitive}, nil
}

func (m searchMatcher) find(text string) (int, bool) {
	if m.re != nil {
		loc := m.re.FindStringIndex(text)
		if loc == nil {
			return 0, false
		}
		return loc[0], true
	}
	haystack := text
	if !m.caseSensitive {
		haystack = strings.ToLower(text)
	}
	idx := strings.Index(haystack, m.literal)
	return idx, idx >= 0
}

func hiddenRelativePath(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return false
	}
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if strings.HasPrefix(part, ".") && part != "." && part != ".." {
			return true
		}
	}
	return false
}

func previewLine(line string) string {
	line = strings.ToValidUTF8(line, "�")
	runes := []rune(line)
	if len(runes) <= maxSearchPreviewRunes {
		return line
	}
	return string(runes[:maxSearchPreviewRunes]) + "…"
}

func (m *searchManager) appendResult(session *searchSession, result workspacesdk.SearchResult, maxResults int) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	if len(session.results) >= maxResults {
		session.info.Truncated = true
		return errSearchLimit
	}
	session.results = append(session.results, result)
	return nil
}

func (m *searchManager) run(ctx context.Context, session *searchSession, req workspacesdk.SearchStartRequest, matcher searchMatcher, maxResults int) {
	defer session.cancel()

	err := afero.Walk(m.fs, req.Root, func(path string, info os.FileInfo, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			if path == req.Root {
				return walkErr
			}
			return nil
		}
		if path != req.Root && !req.IncludeHidden && hiddenRelativePath(req.Root, path) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if req.Mode == "files" {
			if path == req.Root {
				return nil
			}
			rel, relErr := filepath.Rel(req.Root, path)
			if relErr != nil {
				return nil
			}
			if _, ok := matcher.find(rel); ok {
				return m.appendResult(session, workspacesdk.SearchResult{Path: path}, maxResults)
			}
			return nil
		}

		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxSearchFileBytes {
			return nil
		}
		return func() error {
			f, openErr := m.fs.Open(path)
			if openErr != nil {
				return nil
			}
			defer f.Close()

			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 64<<10), maxSearchLineBytes)
			lineNo := 0
			for scanner.Scan() {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				lineNo++
				line := scanner.Text()
				if strings.IndexByte(line, 0) >= 0 {
					return nil
				}
				idx, ok := matcher.find(line)
				if !ok {
					continue
				}
				if err := m.appendResult(session, workspacesdk.SearchResult{
					Path:   path,
					Line:   lineNo,
					Column: idx + 1,
					Text:   previewLine(line),
				}, maxResults); err != nil {
					return err
				}
			}
			if err := scanner.Err(); err != nil {
				// A pathological long/binary line should not abort the entire search.
				return nil
			}
			return nil
		}()
	})

	now := time.Now().Unix()
	session.mu.Lock()
	defer session.mu.Unlock()
	session.info.CompletedAt = &now
	switch {
	case errors.Is(err, errSearchLimit):
		session.info.Status = "complete"
		session.info.Truncated = true
	case errors.Is(err, context.Canceled):
		session.info.Status = "stopped"
	case errors.Is(err, context.DeadlineExceeded):
		session.info.Status = "timeout"
		session.info.Truncated = true
		session.info.Error = "search exceeded 30 second execution limit"
	case err != nil:
		session.info.Status = "error"
		session.info.Error = err.Error()
	default:
		session.info.Status = "complete"
	}
}

func (m *searchManager) get(id, chatID string) (*searchSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked(time.Now())
	session, ok := m.sessions[id]
	if !ok {
		return nil, false
	}
	if session.chatID != "" && session.chatID != chatID {
		return nil, false
	}
	return session, true
}

func (m *searchManager) results(id, chatID string, cursor, limit int) (workspacesdk.SearchResultsResponse, error) {
	if cursor < 0 {
		return workspacesdk.SearchResultsResponse{}, xerrors.New("cursor cannot be negative")
	}
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > maxSearchPage {
		return workspacesdk.SearchResultsResponse{}, xerrors.Errorf("limit must be between 1 and %d", maxSearchPage)
	}
	session, ok := m.get(id, chatID)
	if !ok {
		return workspacesdk.SearchResultsResponse{}, os.ErrNotExist
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	start := cursor
	if start > len(session.results) {
		start = len(session.results)
	}
	end := start + limit
	if end > len(session.results) {
		end = len(session.results)
	}
	results := append([]workspacesdk.SearchResult(nil), session.results[start:end]...)
	info := session.info
	info.ResultCount = len(session.results)
	var next *int
	if end < len(session.results) || info.Status == "running" {
		value := end
		next = &value
	}
	return workspacesdk.SearchResultsResponse{Search: info, Results: results, NextCursor: next}, nil
}

func (m *searchManager) list(chatID string) []workspacesdk.SearchSessionInfo {
	m.mu.Lock()
	m.cleanupLocked(time.Now())
	sessions := make([]*searchSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		if session.chatID == "" || session.chatID == chatID {
			sessions = append(sessions, session)
		}
	}
	m.mu.Unlock()
	infos := make([]workspacesdk.SearchSessionInfo, 0, len(sessions))
	for _, session := range sessions {
		infos = append(infos, session.snapshot())
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].CreatedAt > infos[j].CreatedAt })
	if len(infos) > 50 {
		infos = infos[:50]
	}
	return infos
}

func (m *searchManager) stop(id, chatID string) error {
	session, ok := m.get(id, chatID)
	if !ok {
		return os.ErrNotExist
	}
	session.mu.Lock()
	if session.info.Status != "running" {
		session.mu.Unlock()
		return nil
	}
	cancel := session.cancel
	session.mu.Unlock()
	cancel()
	return nil
}

func (api *API) HandleSearchStart(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req workspacesdk.SearchStartRequest
	if !httpapi.Read(ctx, rw, r, &req) {
		return
	}
	id, err := api.searches.start(req, searchChatID(ctx))
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: err.Error()})
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, workspacesdk.SearchStartResponse{ID: id})
}

func (api *API) HandleSearchResults(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cursor, err := strconv.Atoi(r.URL.Query().Get("cursor"))
	if r.URL.Query().Get("cursor") == "" {
		cursor = 0
		err = nil
	}
	if err != nil || cursor < 0 {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "cursor must be a non-negative integer"})
		return
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if r.URL.Query().Get("limit") == "" {
		limit = 100
		err = nil
	}
	if err != nil || limit < 1 || limit > maxSearchPage {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "limit must be between 1 and 1000"})
		return
	}
	resp, err := api.searches.results(chi.URLParam(r, "id"), searchChatID(ctx), cursor, limit)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		httpapi.Write(ctx, rw, status, codersdk.Response{Message: err.Error()})
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, resp)
}

func (api *API) HandleSearchList(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	httpapi.Write(ctx, rw, http.StatusOK, workspacesdk.ListSearchesResponse{Searches: api.searches.list(searchChatID(ctx))})
}

func (api *API) HandleSearchStop(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := api.searches.stop(chi.URLParam(r, "id"), searchChatID(ctx)); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		httpapi.Write(ctx, rw, status, codersdk.Response{Message: err.Error()})
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, codersdk.Response{Message: "Search stop requested."})
}
