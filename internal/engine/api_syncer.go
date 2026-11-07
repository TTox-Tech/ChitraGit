package engine

// Re-exports for sync results and progress events, so the UI packages
// reach internal/syncer only through engine.

import "github.com/TTox-Tech/chitragit/internal/syncer"

type (
	SyncEvent  = syncer.Event
	SyncResult = syncer.Result
)

const (
	Failed            = syncer.Failed
	Skipped           = syncer.Skipped
	Updated           = syncer.Updated
	Unchanged         = syncer.Unchanged
	PhaseChanged      = syncer.PhaseChanged
	PhasePreflight    = syncer.PhasePreflight
	RepoFinished      = syncer.RepoFinished
	RepoStarted       = syncer.RepoStarted
	StatusAhead       = syncer.StatusAhead
	StatusBlocked     = syncer.StatusBlocked
	StatusBusy        = syncer.StatusBusy
	StatusConflict    = syncer.StatusConflict
	StatusDetached    = syncer.StatusDetached
	StatusDirty       = syncer.StatusDirty
	StatusDiverged    = syncer.StatusDiverged
	StatusFailed      = syncer.StatusFailed
	StatusNoUpstream  = syncer.StatusNoUpstream
	StatusOK          = syncer.StatusOK
	StatusRecovered   = syncer.StatusRecovered
	StatusRewritten   = syncer.StatusRewritten
	StatusUpstreamGon = syncer.StatusUpstreamGon
)
