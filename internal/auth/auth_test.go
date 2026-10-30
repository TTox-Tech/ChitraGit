package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

type memKeyring struct {
	m       map[string]string
	broken  bool // like headless Linux: no Secret Service
	setHits int
}

func (k *memKeyring) Get(_, u string) (string, error) {
	if k.broken {
		return "", errors.New("no dbus")
	}
	if v, ok := k.m[u]; ok {
		return v, nil
	}
	return "", keyring.ErrNotFound
}

func (k *memKeyring) Set(_, u, p string) error {
	k.setHits++
	if k.broken {
		return errors.New("no dbus")
	}
	k.m[u] = p
	return nil
}

func (k *memKeyring) Delete(_, u string) error {
	if k.broken {
		return errors.New("no dbus")
	}
	if _, ok := k.m[u]; !ok {
		return keyring.ErrNotFound
	}
	delete(k.m, u)
	return nil
}

func newStore(t *testing.T, kr *memKeyring, env map[string]string) *Store {
	t.Helper()
	return &Store{Dir: t.TempDir(), Keyring: kr, Getenv: func(k string) string { return env[k] }}
}

func TestLookupOrder(t *testing.T) {
	ctx := context.Background()
	kr := &memKeyring{m: map[string]string{"github.com": "from-keychain"}}
	s := newStore(t, kr, map[string]string{"GITHUB_TOKEN": "from-env"})
	s.GH = func(context.Context, string) string { return "from-gh" }
	if tok := s.Lookup(ctx, "github", "github.com"); tok.Value != "from-env" || tok.Var != "GITHUB_TOKEN" {
		t.Fatalf("env should win: %+v", tok)
	}

	s = newStore(t, kr, nil)
	s.GH = func(context.Context, string) string { return "from-gh" }
	if tok := s.Lookup(ctx, "github", "GitHub.com"); tok.Value != "from-keychain" {
		t.Fatalf("keychain: %+v", tok)
	}
	if tok := s.Lookup(ctx, "github", "ghe.corp"); tok.Source != FromGH {
		t.Fatalf("gh fallback: %+v", tok)
	}
	if tok := s.Lookup(ctx, "gitlab", "gitlab.com"); tok.Value != "" {
		t.Fatalf("gh must only serve GitHub: %+v", tok)
	}
}

func TestWellKnownEnvVarsStayOnTheirHost(t *testing.T) {
	s := newStore(t, &memKeyring{m: map[string]string{}}, map[string]string{
		"GITLAB_TOKEN": "public", "CHITRAGIT_TOKEN_GIT_CORP_IO": "private"})
	if tok := s.Lookup(context.Background(), "gitlab", "gitlab.example.com"); tok.Value != "" {
		t.Fatalf("a gitlab.com token must not go to another server: %+v", tok)
	}
	if tok := s.Lookup(context.Background(), "gitlab", "git.corp.io"); tok.Value != "private" {
		t.Fatalf("per-host variable: %+v", tok)
	}
}

func TestSaveFallsBackToPrivateFile(t *testing.T) {
	kr := &memKeyring{m: map[string]string{}, broken: true}
	s := newStore(t, kr, nil)
	where, err := s.Save("codeberg.org", "tok")
	if err != nil || where != FromFile {
		t.Fatalf("Save = %s, %v", where, err)
	}
	info, err := os.Stat(s.File())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("token file mode %v, want 0600", info.Mode().Perm())
	}
	if tok := s.Lookup(context.Background(), "gitea", "codeberg.org"); tok.Value != "tok" || tok.Source != FromFile {
		t.Fatalf("Lookup = %+v", tok)
	}
	if err := s.Delete("codeberg.org"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.File()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty token file should be removed: %v", err)
	}
	if tok := s.Lookup(context.Background(), "gitea", "codeberg.org"); tok.Value != "" {
		t.Fatalf("after delete: %+v", tok)
	}
}

func TestSaveToKeychainClearsFileCopy(t *testing.T) {
	kr := &memKeyring{m: map[string]string{}, broken: true}
	s := newStore(t, kr, nil)
	_, _ = s.Save("gitlab.com", "old")
	kr.broken = false
	if where, err := s.Save("gitlab.com", "new"); err != nil || where != FromKeychain {
		t.Fatalf("Save = %s %v", where, err)
	}
	if _, err := os.Stat(s.File()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the file copy should be gone once the keychain has the token")
	}
}

func TestDeviceFlow(t *testing.T) {
	polls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/device/code", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("client_id") != "cid" {
			t.Errorf("client_id = %q", r.Form.Get("client_id"))
		}
		_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"ABCD-1234","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`))
	})
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, _ *http.Request) {
		polls++
		switch polls {
		case 1:
			_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
		case 2:
			_, _ = w.Write([]byte(`{"error":"slow_down","interval":10}`))
		default:
			_, _ = w.Write([]byte(`{"access_token":"gho_x","token_type":"bearer"}`))
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var waits []time.Duration
	d := &Device{ClientID: "cid", Base: srv.URL, sleep: func(_ context.Context, w time.Duration) error {
		waits = append(waits, w)
		return nil
	}}
	var shown string
	tok, err := d.Login(context.Background(), func(c DeviceCode) { shown = c.UserCode })
	if err != nil || tok != "gho_x" || shown != "ABCD-1234" {
		t.Fatalf("Login = %q, %v (shown %q)", tok, err, shown)
	}
	if len(waits) != 3 || waits[2] != 10*time.Second {
		t.Fatalf("waits = %v; slow_down should raise the interval", waits)
	}

	if _, err := (&Device{}).Login(context.Background(), nil); err == nil {
		t.Fatal("no client ID must fail with a hint")
	}
}
