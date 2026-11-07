package engine

// Re-exports for provider sign-in, so the UI packages reach internal/auth
// only through engine.

import "github.com/TTox-Tech/chitragit/internal/auth"

type (
	Device     = auth.Device
	DeviceCode = auth.DeviceCode
	AuthStore  = auth.Store
)

const (
	TokenFromFile = auth.FromFile
)

func NewAuthStore(dir string) *auth.Store { return auth.New(dir) }

// GitHubClientID is the OAuth App this build signs in to GitHub with.
func GitHubClientID() string { return auth.GitHubClientID }
