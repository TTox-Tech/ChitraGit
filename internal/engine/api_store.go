package engine

// Re-exports for the archive database and the data the UI shows from it,
// so the UI packages reach internal/store only through engine.

import (
	"context"

	"github.com/TTox-Tech/chitragit/internal/store"
)

type (
	RepoNote       = store.RepoNote
	Store          = store.Store
	Activity       = store.Activity
	ActivityFilter = store.ActivityFilter
	Conflict       = store.Conflict
	FindingFilter  = store.FindingFilter
	Health         = store.Health
	RepoInfo       = store.RepoInfo
	RepoRun        = store.RepoRun
	RunSummary     = store.RunSummary
	TagCount       = store.TagCount
)

const (
	CardNeverSync  = store.CardNeverSync
	CardDiverged   = store.CardDiverged
	ActivityRefs   = store.ActivityRefs
	CardFailing    = store.CardFailing
	CardFavourites = store.CardFavourites
	CardLocked     = store.CardLocked
	CardMissing    = store.CardMissing
	CardRescued    = store.CardRescued
)

var (
	HealthCards = store.Cards
	ErrNoRepo   = store.ErrNoRepo
)

func OpenStore(ctx context.Context, dbPath string) (*store.Store, error) {
	return store.Open(ctx, dbPath)
}

func NormalizeTag(t string) string { return store.NormalizeTag(t) }
