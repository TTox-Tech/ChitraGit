package platform

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// OnNetworkFS reports whether dir is on a UNC share or a mapped network
// drive. It errs towards false: a path it cannot inspect is treated as local.
func OnNetworkFS(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	vol := filepath.VolumeName(abs)
	if strings.HasPrefix(vol, `\\`) {
		return true
	}
	root, err := windows.UTF16PtrFromString(vol + `\`)
	if err != nil {
		return false
	}
	return windows.GetDriveType(root) == windows.DRIVE_REMOTE
}
