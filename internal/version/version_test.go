package version

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"testing"
)

func TestModuleVersion(t *testing.T) {
	cases := map[string]string{
		"v0.3.0":                             "0.3.0",
		"v1.0.0-rc.1":                        "1.0.0-rc.1",
		"(devel)":                            "",
		"":                                   "",
		"v0.0.0-20261020222038-f7dd85fde697": "0.0.0-20261020222038-f7dd85fde697",
	}
	for in, want := range cases {
		info := &debug.BuildInfo{Main: debug.Module{Version: in}}
		if got := moduleVersion(info); got != want {
			t.Errorf("moduleVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		latest, current string
		want            bool
	}{
		{"1.0.1", "1.0.0", true},
		{"v1.1.0", "1.0.9", true},
		{"2.0.0", "1.99.99", true},
		{"1.0.0", "1.0.0", false},
		{"1.0.0", "1.0.1", false},
		{"1.0.0", "1.0.0-rc.3", true},
		{"1.0.0-rc.10", "1.0.0-rc.9", true},
		{"1.0.0-rc.2", "1.0.0-rc.2", false},
		{"1.0.0", "dev", false},
		{"garbage", "1.0.0", false},
		{"1.0.0+build.5", "1.0.0", false},
	} {
		if got := Newer(c.latest, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestLatest(t *testing.T) {
	var agent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent = r.UserAgent()
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.3","html_url":"https://github.com/TTox-Tech/ChitraGit/releases/tag/v1.2.3","body":"notes"}`))
	}))
	defer srv.Close()
	old := LatestURL
	t.Cleanup(func() { LatestURL = old })
	LatestURL = srv.URL
	rel, err := Latest(context.Background(), srv.Client())
	if err != nil || rel.Version != "1.2.3" || !strings.HasSuffix(rel.URL, "/v1.2.3") {
		t.Fatalf("Latest = %+v, %v", rel, err)
	}
	if !strings.HasPrefix(agent, "chitragit/") {
		t.Errorf("user agent = %q", agent)
	}

	for name, h := range map[string]http.HandlerFunc{
		"not found": http.NotFound,
		"bad tag":   func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"tag_name":"nightly"}`)) },
		"not json":  func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`<html>`)) },
	} {
		bad := httptest.NewServer(h)
		LatestURL = bad.URL
		if _, err := Latest(context.Background(), bad.Client()); err == nil {
			t.Errorf("%s: no error", name)
		}
		bad.Close()
	}
}
