package engine

// Re-exports for resolving sync conflicts, so the UI packages reach
// internal/conflict only through engine.

import "github.com/TTox-Tech/chitragit/internal/conflict"

type (
	Choice         = conflict.Choice
	ConflictFile   = conflict.File
	Hunk           = conflict.Hunk
	ResolveSession = conflict.Session
)

const (
	Both   = conflict.Both
	Mine   = conflict.Mine
	Theirs = conflict.Theirs
)

var (
	ErrNothingToResolve = conflict.ErrNothingToResolve
)

func ParseChoice(s string) (conflict.Choice, bool) { return conflict.ParseChoice(s) }
