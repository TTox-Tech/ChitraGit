package platform

import "golang.org/x/sys/unix"

// OnNetworkFS reports whether dir is on an NFS or SMB/CIFS share. It errs
// towards false: a directory it cannot inspect is treated as local.
func OnNetworkFS(dir string) bool {
	var st unix.Statfs_t
	if unix.Statfs(dir, &st) != nil {
		return false
	}
	return isNetworkFSType(uint32(st.Type)) // uint32: f_type is signed on 32-bit systems
}

func isNetworkFSType(t uint32) bool {
	switch t {
	case 0x6969, // NFS
		0x517B,     // SMB
		0xFF534D42, // CIFS
		0xFE534D42: // SMB2
		return true
	}
	return false
}
