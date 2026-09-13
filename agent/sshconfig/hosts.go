package sshconfig

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/xerrors"
)

// Host describes an exact SSH alias explicitly declared by a Host directive.
// Wildcard and negated patterns are intentionally excluded from the safe MCP
// remote-target surface.
type Host struct {
	Alias string `json:"alias"`
}

// List returns exact aliases declared by the current user's SSH config and its
// Include files. A missing ~/.ssh/config is treated as an empty host list.
func List() ([]Host, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, xerrors.Errorf("resolve user home: %w", err)
	}
	return ListFromFile(filepath.Join(home, ".ssh", "config"), home)
}

// ListFromFile is exposed for deterministic tests.
func ListFromFile(configPath, home string) ([]Host, error) {
	aliases := map[string]string{}
	seen := map[string]bool{}
	if err := collect(configPath, home, aliases, seen); err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(aliases))
	for key := range aliases {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]Host, 0, len(keys))
	for _, key := range keys {
		result = append(result, Host{Alias: aliases[key]})
	}
	return result, nil
}

// ValidateAlias ensures host is an exact alias present in the current user's
// SSH config. MCP callers cannot use arbitrary hostnames, IPs, user@host
// strings, or wildcard-only config matches.
func ValidateAlias(host string) error {
	host = strings.TrimSpace(host)
	if !safeAlias(host) {
		return xerrors.New("host must be a configured exact SSH alias containing only letters, digits, '.', '_' or '-'; call remote_hosts to list allowed targets")
	}
	hosts, err := List()
	if err != nil {
		return err
	}
	for _, configured := range hosts {
		if strings.EqualFold(configured.Alias, host) {
			return nil
		}
	}
	return xerrors.Errorf("SSH alias %q is not configured; call remote_hosts to list allowed targets", host)
}

func collect(configPath, home string, aliases map[string]string, seen map[string]bool) error {
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return xerrors.Errorf("resolve SSH config path: %w", err)
	}
	if seen[abs] {
		return nil
	}
	seen[abs] = true

	file, err := os.Open(abs)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return xerrors.Errorf("open SSH config %q: %w", abs, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields, err := sshFields(scanner.Text())
		if err != nil || len(fields) == 0 {
			continue
		}
		keyword := fields[0]
		args := fields[1:]
		if key, value, ok := strings.Cut(keyword, "="); ok {
			keyword = key
			if value != "" {
				args = append([]string{value}, args...)
			}
		}
		if len(args) > 0 && args[0] == "=" {
			args = args[1:]
		}
		if len(args) == 0 {
			continue
		}
		switch strings.ToLower(keyword) {
		case "host":
			for _, candidate := range args {
				if !safeAlias(candidate) || strings.ContainsAny(candidate, "*?![") {
					continue
				}
				key := strings.ToLower(candidate)
				if _, exists := aliases[key]; !exists {
					aliases[key] = candidate
				}
			}
		case "include":
			for _, include := range args {
				for _, match := range resolveIncludes(include, abs, home) {
					if err := collect(match, home, aliases, seen); err != nil {
						return err
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return xerrors.Errorf("read SSH config %q: %w", abs, err)
	}
	return nil
}

func resolveIncludes(pattern, configPath, home string) []string {
	switch {
	case pattern == "~":
		pattern = home
	case strings.HasPrefix(pattern, "~/"):
		pattern = filepath.Join(home, strings.TrimPrefix(pattern, "~/"))
	case !filepath.IsAbs(pattern):
		pattern = filepath.Join(filepath.Dir(configPath), pattern)
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	sort.Strings(matches)
	return matches
}

func safeAlias(alias string) bool {
	if alias == "" {
		return false
	}
	for _, r := range alias {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// sshFields tokenizes the subset of ssh_config syntax needed by Host and
// Include directives, including single/double quotes, backslash escapes, and
// comments outside quotes.
func sshFields(line string) ([]string, error) {
	var fields []string
	var current strings.Builder
	quote := rune(0)
	escaped := false
	flush := func() {
		if current.Len() == 0 {
			return
		}
		fields = append(fields, current.String())
		current.Reset()
	}
	for _, r := range line {
		if escaped {
			_, _ = current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				_, _ = current.WriteRune(r)
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case '#':
			flush()
			return fields, nil
		case ' ', '\t', '\r', '\n':
			flush()
		default:
			_, _ = current.WriteRune(r)
		}
	}
	if escaped {
		_, _ = current.WriteRune('\\')
	}
	if quote != 0 {
		return nil, xerrors.New("unterminated quote")
	}
	flush()
	return fields, nil
}
