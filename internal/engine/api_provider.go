package engine

// Re-exports for hosting providers and remote URLs, so the UI packages
// reach internal/provider only through engine.

import "github.com/TTox-Tech/chitragit/internal/provider"

type (
	ProviderRegistry = provider.Registry
)

const (
	ProviderGeneric = provider.Generic
	ProviderGitHub  = provider.GitHub
	ProviderGitLab  = provider.GitLab
)

func ParseRemoteURL(raw string) provider.Remote { return provider.Parse(raw) }

func ParseSource(spec string) (provider.Source, error) { return provider.ParseSource(spec) }
