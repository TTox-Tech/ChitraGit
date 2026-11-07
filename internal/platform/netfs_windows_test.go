package platform

import "testing"

func TestOnNetworkFSWindows(t *testing.T) {
	if !OnNetworkFS(`\\server\share\archive`) {
		t.Error("a UNC path is a network share")
	}
	if OnNetworkFS(t.TempDir()) {
		t.Error("a temp dir is not a network share")
	}
}
