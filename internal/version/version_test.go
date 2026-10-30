package version

import (
	"runtime/debug"
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
