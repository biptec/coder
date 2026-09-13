package agentfiles

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"path"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

func (m *searchManager) runRemote(ctx context.Context, session *searchSession, req workspacesdk.SearchStartRequest, matcher searchMatcher, maxResults int) {
	defer session.cancel()

	var err error
	if req.Mode == "files" {
		err = m.runRemoteFileSearch(ctx, session, req, matcher, maxResults)
	} else {
		err = m.runRemoteContentSearch(ctx, session, req, matcher, maxResults)
	}

	now := time.Now().Unix()
	session.mu.Lock()
	defer session.mu.Unlock()
	session.info.CompletedAt = &now
	switch {
	case errors.Is(err, errSearchLimit):
		session.info.Status = "complete"
		session.info.Truncated = true
	case errors.Is(err, context.Canceled), errors.Is(ctx.Err(), context.Canceled):
		session.info.Status = "stopped"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(ctx.Err(), context.DeadlineExceeded):
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

func (m *searchManager) runRemoteFileSearch(ctx context.Context, session *searchSession, req workspacesdk.SearchStartRequest, matcher searchMatcher, maxResults int) error {
	command := "test -d " + remoteShellQuote(req.Root) + " && find " + remoteShellQuote(req.Root) + " -print0"
	cmd, err := remoteSSHCommand(ctx, m.execer, req.Host, req.IdentityFile, command)
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return xerrors.Errorf("remote search stdout: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return xerrors.Errorf("start remote search: %w", err)
	}

	reader := bufio.NewReader(stdout)
	var parseErr error
	for {
		raw, readErr := reader.ReadBytes(0)
		if len(raw) > 0 {
			remotePath := strings.TrimSuffix(string(raw), "\x00")
			if remotePath != req.Root {
				rel, relErr := filepath.Rel(req.Root, remotePath)
				if relErr == nil && (req.IncludeHidden || !remoteHiddenRelativePath(rel)) {
					if _, ok := matcher.find(rel); ok {
						if appendErr := m.appendResult(session, workspacesdk.SearchResult{Path: remotePath}, maxResults); appendErr != nil {
							parseErr = appendErr
							session.cancel()
							break
						}
					}
				}
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				parseErr = readErr
			}
			break
		}
	}
	waitErr := cmd.Wait()
	if parseErr != nil {
		return parseErr
	}
	if waitErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = waitErr.Error()
		}
		return xerrors.Errorf("remote file search failed: %s", message)
	}
	return nil
}

func (m *searchManager) runRemoteContentSearch(ctx context.Context, session *searchSession, req workspacesdk.SearchStartRequest, matcher searchMatcher, maxResults int) error {
	root := path.Clean(req.Root)
	parent := path.Dir(root)
	base := path.Base(root)
	command := "test -e " + remoteShellQuote(root) + " && tar -cf - -C " + remoteShellQuote(parent) + " " + remoteShellQuote(base)
	cmd, err := remoteSSHCommand(ctx, m.execer, req.Host, req.IdentityFile, command)
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return xerrors.Errorf("remote search stdout: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return xerrors.Errorf("start remote search: %w", err)
	}

	tr := tar.NewReader(stdout)
	var parseErr error
	for {
		hdr, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			parseErr = xerrors.Errorf("read remote search archive: %w", nextErr)
			break
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}
		if hdr.Size > maxSearchFileBytes {
			continue
		}
		remotePath := path.Join(parent, path.Clean(hdr.Name))
		rel, relErr := filepath.Rel(root, remotePath)
		if relErr != nil {
			continue
		}
		if !req.IncludeHidden && remoteHiddenRelativePath(rel) {
			continue
		}

		scanner := bufio.NewScanner(tr)
		scanner.Buffer(make([]byte, 64<<10), maxSearchLineBytes)
		lineNo := 0
		for scanner.Scan() {
			if ctx.Err() != nil {
				parseErr = ctx.Err()
				break
			}
			lineNo++
			line := scanner.Text()
			if strings.IndexByte(line, 0) >= 0 {
				break
			}
			idx, ok := matcher.find(line)
			if !ok {
				continue
			}
			if appendErr := m.appendResult(session, workspacesdk.SearchResult{
				Path:   remotePath,
				Line:   lineNo,
				Column: idx + 1,
				Text:   previewLine(line),
			}, maxResults); appendErr != nil {
				parseErr = appendErr
				break
			}
		}
		if parseErr != nil {
			session.cancel()
			break
		}
		// Long or binary-like lines are skipped just like local search.
		if scanErr := scanner.Err(); scanErr != nil {
			continue
		}
	}
	waitErr := cmd.Wait()
	if parseErr != nil {
		return parseErr
	}
	if waitErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = waitErr.Error()
		}
		return xerrors.Errorf("remote content search failed: %s", message)
	}
	return nil
}

func remoteHiddenRelativePath(rel string) bool {
	if rel == "." || rel == "" {
		return false
	}
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") && part != "." && part != ".." {
			return true
		}
	}
	return false
}
