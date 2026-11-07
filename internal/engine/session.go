package engine

import (
	"context"
	"time"

	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/platform"
)

// Git finds git, runs it with an empty hooks dir, and checks its version.
// A zero timeout means no limit. When only the version check fails, the
// runner is still returned so callers like doctor can keep probing.
func Git(ctx context.Context, timeout time.Duration) (*gitx.Runner, gitx.Version, error) {
	hooks, err := platform.EmptyHooksDir()
	if err != nil {
		return nil, gitx.Version{}, err
	}
	g, err := gitx.New(gitx.Options{HooksDir: hooks, Timeout: timeout})
	if err != nil {
		return nil, gitx.Version{}, err
	}
	v, err := g.CheckVersion(ctx)
	return g, v, err
}
