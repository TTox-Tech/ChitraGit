package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/version"
)

func TestVersionCheck(t *testing.T) {
	isolate(t)
	asked := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked++
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.0","html_url":"https://github.com/TTox-Tech/ChitraGit/releases/tag/v1.2.0"}`))
	}))
	defer srv.Close()
	oldURL, oldVersion := version.LatestURL, version.Version
	t.Cleanup(func() { version.LatestURL, version.Version = oldURL, oldVersion })
	version.LatestURL = srv.URL

	for current, want := range map[string]string{
		"1.1.0":       "A newer release is available: 1.2.0",
		"1.2.0":       "This is the latest release (1.2.0)",
		"1.3.0-rc.1":  "newer than the latest release",
		"dev":         "The latest release is 1.2.0",
		"1.2.0-rc.3":  "A newer release is available: 1.2.0",
		"0.9.0+local": "A newer release is available",
	} {
		version.Version = current
		code, out, errOut := run(t, "version", "--check")
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("%s: exit %d, out %q, err %q; want %q", current, code, out, errOut, want)
		}
	}

	version.Version = "1.1.0"
	code, out, _ := run(t, "--json", "version", "--check")
	var got struct {
		Latest string `json:"latest"`
		Update bool   `json:"update_available"`
	}
	if err := json.Unmarshal([]byte(out), &got); code != 0 || err != nil || got.Latest != "1.2.0" || !got.Update {
		t.Errorf("json: exit %d, %+v, %v\n%s", code, got, err, out)
	}

	// Plain `version` never asks, and the setting turns --check off.
	before := asked
	if code, _, _ := run(t, "version"); code != 0 || asked != before {
		t.Errorf("plain version asked GitHub (exit %d)", code)
	}
	t.Setenv("CHITRAGIT_UI_UPDATE_CHECK", "false")
	if code, _, errOut := run(t, "version", "--check"); code == 0 || asked != before || !strings.Contains(errOut, "turned off") {
		t.Errorf("check with update_check off: exit %d, asked %d, %q", code, asked-before, errOut)
	}
}
