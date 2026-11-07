package platform

import "testing"

func TestNetworkFSName(t *testing.T) {
	for name, want := range map[string]bool{"nfs": true, "smbfs": true, "afpfs": true, "webdav": true, "apfs": false, "hfs": false} {
		if got := isNetworkFSName(name); got != want {
			t.Errorf("isNetworkFSName(%q) = %v, want %v", name, got, want)
		}
	}
	if OnNetworkFS(t.TempDir()) {
		t.Error("a temp dir is not a network share")
	}
}
