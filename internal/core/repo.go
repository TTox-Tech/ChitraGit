package core

// Repo kinds, as detected by the scanner (PRD F1).
const (
	KindNormal   = "normal"   // working tree with a .git folder (or a .git file pointing to a separate git dir)
	KindBare     = "bare"     // no working tree: HEAD, objects/ and refs/ in the folder itself
	KindWorktree = "worktree" // linked worktree of another repo; synced through its main repo
	KindNested   = "nested"   // independent repo inside another repo's working tree
)

// Repo is one repository known to the archive.
type Repo struct {
	ID           string `json:"id"`
	Path         string `json:"path"` // relative to the archive root, '/' separated
	Kind         string `json:"kind"`
	Provider     string `json:"provider,omitempty"`
	Host         string `json:"host,omitempty"`
	Owner        string `json:"owner,omitempty"`
	Name         string `json:"name,omitempty"`
	Status       string `json:"status"`
	StatusReason string `json:"status_reason,omitempty"`
	Favourite    bool   `json:"favourite"`
	Missing      bool   `json:"missing"`
	LastSyncedAt string `json:"last_synced_at,omitempty"`

	// Locked repos are delete-protected; every rescued repo is locked.
	Locked bool `json:"locked"`
	// Rescued: upstream vanished and this copy is now the one that matters.
	Rescued          bool   `json:"rescued"`
	RescueReason     string `json:"rescue_reason,omitempty"`
	ArchivedUpstream bool   `json:"archived_upstream"`

	// Remotes maps remote name to URL. Filled by the scanner for new repos.
	Remotes map[string]string `json:"-"`
}

// RefChange is one ref that a sync moved. It feeds the activity feed.
type RefChange struct {
	Ref     string `json:"ref"`
	OldSHA  string `json:"old,omitempty"` // empty = new ref
	NewSHA  string `json:"new,omitempty"` // empty = deleted upstream
	Commits int    `json:"commits,omitempty"`
	Kind    string `json:"kind"` // update | create | delete | rewrite
}
