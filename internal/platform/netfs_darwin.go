package platform

import "golang.org/x/sys/unix"

// OnNetworkFS reports whether dir is on an NFS, SMB, AFP or WebDAV share. It
// errs towards false: a directory it cannot inspect is treated as local.
func OnNetworkFS(dir string) bool {
	var st unix.Statfs_t
	if unix.Statfs(dir, &st) != nil {
		return false
	}
	return isNetworkFSName(unix.ByteSliceToString(st.Fstypename[:]))
}

func isNetworkFSName(name string) bool {
	switch name {
	case "nfs", "smbfs", "afpfs", "webdav":
		return true
	}
	return false
}
