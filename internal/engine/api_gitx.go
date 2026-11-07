package engine

// Re-exports for the git runner, so the UI packages reach internal/gitx
// only through engine.

import "github.com/TTox-Tech/chitragit/internal/gitx"

type (
	GitRunner = gitx.Runner
)
