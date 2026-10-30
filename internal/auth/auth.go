// Package auth finds and stores provider API tokens (PRD F6).
//
// Tokens come from, in order: environment variables, the OS keychain
// (macOS Keychain, Windows Credential Manager, Secret Service on Linux), a
// 0600 fallback file when no keychain is available, and finally the GitHub
// CLI (`gh auth token`). Tokens are only ever sent to provider APIs: never
// to git, and never written to logs, config files or the archive database.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
)

// service is the keychain service name; the account is the host.
const service = "chitragit"

// Where a token came from, for `auth status`.
const (
	FromEnv      = "env"
	FromKeychain = "keychain"
	FromFile     = "file"
	FromGH       = "gh"
)

// Keyring is the part of the OS keychain the store uses.
type Keyring interface {
	Get(service, user string) (string, error)
	Set(service, user, password string) error
	Delete(service, user string) error
}

type osKeyring struct{}

func (osKeyring) Get(s, u string) (string, error) { return keyring.Get(s, u) }
func (osKeyring) Set(s, u, p string) error        { return keyring.Set(s, u, p) }
func (osKeyring) Delete(s, u string) error        { return keyring.Delete(s, u) }

// Store looks tokens up and saves them. The zero value is not usable;
// call New.
type Store struct {
	// Dir holds the fallback token file (the user config dir).
	Dir     string
	Keyring Keyring
	Getenv  func(string) string
	// GH runs `gh auth token`; nil disables it.
	GH func(ctx context.Context, host string) string

	mu    sync.Mutex
	cache map[string]Token
}

// Token is a token and where it came from.
type Token struct {
	Value  string
	Source string // FromEnv, FromKeychain, FromFile or FromGH
	Var    string // the environment variable, for FromEnv
}

// New returns a store backed by the OS keychain, with its fallback file
// in dir.
func New(dir string) *Store {
	return &Store{Dir: dir, Keyring: osKeyring{}, Getenv: os.Getenv, GH: ghToken}
}

// EnvVars lists the environment variables checked for a host, most
// specific first. The well-known variables only apply to the public host
// they belong to, so a token for one server is never sent to another.
func EnvVars(kind, host string) []string {
	vars := []string{"CHITRAGIT_TOKEN_" + envName(host)}
	switch {
	case host == "github.com":
		vars = append(vars, "GH_TOKEN", "GITHUB_TOKEN")
	case kind == "github":
		vars = append(vars, "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN")
	case host == "gitlab.com":
		vars = append(vars, "GITLAB_TOKEN")
	case host == "codeberg.org":
		vars = append(vars, "CODEBERG_TOKEN")
	case host == "gitea.com":
		vars = append(vars, "GITEA_TOKEN")
	}
	return vars
}

// envName turns "git.example.com" into "GIT_EXAMPLE_COM".
func envName(host string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(host) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// Lookup returns the token for a host, or a zero Token. Results are
// cached, so a run asks the keychain once per host.
func (s *Store) Lookup(ctx context.Context, kind, host string) Token {
	host = strings.ToLower(host)
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.cache[host]; ok {
		return t
	}
	t := s.lookup(ctx, kind, host)
	if s.cache == nil {
		s.cache = map[string]Token{}
	}
	s.cache[host] = t
	return t
}

func (s *Store) lookup(ctx context.Context, kind, host string) Token {
	for _, v := range EnvVars(kind, host) {
		if tok := strings.TrimSpace(s.Getenv(v)); tok != "" {
			return Token{Value: tok, Source: FromEnv, Var: v}
		}
	}
	if tok, err := s.Keyring.Get(service, host); err == nil && tok != "" {
		return Token{Value: tok, Source: FromKeychain}
	}
	if tok := s.readFile()[host]; tok != "" {
		return Token{Value: tok, Source: FromFile}
	}
	if kind == "github" && s.GH != nil {
		if tok := s.GH(ctx, host); tok != "" {
			return Token{Value: tok, Source: FromGH}
		}
	}
	return Token{}
}

// Save stores a token in the keychain, or in the fallback file when no
// keychain is available. It reports where the token went.
func (s *Store) Save(host, token string) (string, error) {
	host = strings.ToLower(host)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cache, host)
	if err := s.Keyring.Set(service, host, token); err == nil {
		// A stale copy in the file would be found if the keychain is
		// locked later; keep one place only.
		return FromKeychain, s.updateFile(host, "")
	}
	return FromFile, s.updateFile(host, token)
}

// Delete removes a saved token from the keychain and the fallback file.
// Environment variables and gh are left alone.
func (s *Store) Delete(host string) error {
	host = strings.ToLower(host)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cache, host)
	err := s.Keyring.Delete(service, host)
	if errors.Is(err, keyring.ErrNotFound) {
		err = nil
	}
	if ferr := s.updateFile(host, ""); ferr != nil {
		return ferr
	}
	if err != nil && s.readFile()[host] == "" {
		// No keychain here (headless Linux): nothing could be in it.
		return nil
	}
	return err
}

// File is the fallback token file.
func (s *Store) File() string { return filepath.Join(s.Dir, "tokens.json") }

func (s *Store) readFile() map[string]string {
	data, err := os.ReadFile(s.File())
	m := map[string]string{}
	if err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}

// updateFile sets (or with an empty token, removes) a host in the file,
// writing a new file and renaming it so a crash never leaves half a file.
func (s *Store) updateFile(host, token string) error {
	m := s.readFile()
	if token == "" {
		if _, ok := m[host]; !ok {
			return nil
		}
		delete(m, host)
	} else {
		m[host] = token
	}
	if len(m) == 0 {
		err := os.Remove(s.File())
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(m, "", "  ")
	tmp := s.File() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.File())
}

// ghToken asks the GitHub CLI for its token, if gh is installed and
// logged in to host.
func ghToken(ctx context.Context, host string) string {
	bin, err := exec.LookPath("gh")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "auth", "token", "--hostname", host).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
