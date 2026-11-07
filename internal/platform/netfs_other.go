//go:build !linux && !darwin && !windows

package platform

// OnNetworkFS always reports false on systems without a detection.
// shortcut: no network check on BSDs; add statfs f_fstypename there if asked.
func OnNetworkFS(dir string) bool { return false }
