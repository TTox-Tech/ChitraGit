package platform

import "testing"

func TestNetworkFSType(t *testing.T) {
	for typ, want := range map[uint32]bool{0x6969: true, 0xFF534D42: true, 0xFE534D42: true, 0x517B: true, 0xEF53: false, 0x01021994: false} {
		if got := isNetworkFSType(typ); got != want {
			t.Errorf("isNetworkFSType(%#x) = %v, want %v", typ, got, want)
		}
	}
	if OnNetworkFS(t.TempDir()) {
		t.Error("a temp dir is not a network share")
	}
}
