package engine

// Re-exports for the git runner, so the UI packages reach internal/gitx
// only through engine.

import "github.com/TTox-Tech/chitragit/internal/gitx"

type (
	GitRunner  = gitx.Runner
	GitVersion = gitx.Version

	// Reading commits and diffs (CG-35b), called as GitRunner methods.
	CommitDetail = gitx.CommitDetail
	DiffOptions  = gitx.DiffOptions
	DiffResult   = gitx.DiffResult
	FileChange   = gitx.FileChange

	BlameOptions = gitx.BlameOptions
	BlameResult  = gitx.BlameResult
)
