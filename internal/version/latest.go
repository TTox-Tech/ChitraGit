package version

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// LatestURL is the GitHub API endpoint for ChitraGit's latest release. It
// never lists pre-releases, so a release candidate is not offered as an
// update to people on a stable version.
var LatestURL = "https://api.github.com/repos/TTox-Tech/ChitraGit/releases/latest"

// Release is a published ChitraGit release.
type Release struct {
	Version string `json:"version"` // without the "v"
	URL     string `json:"url"`
}

// Latest asks GitHub for the latest release. It sends no data about the
// archive, only a request with ChitraGit's version as the user agent.
func Latest(ctx context.Context, client *http.Client) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, LatestURL, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "chitragit/"+Version)
	resp, err := client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var body struct {
		Tag string `json:"tag_name"`
		URL string `json:"html_url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return Release{}, fmt.Errorf("read the latest release: %w", err)
	}
	v := strings.TrimPrefix(body.Tag, "v")
	if _, ok := parseSemver(v); !ok {
		return Release{}, fmt.Errorf("the latest release has an unexpected tag %q", body.Tag)
	}
	return Release{Version: v, URL: body.URL}, nil
}

// Newer reports whether release version latest is newer than current. A
// current version that is not a release (a local "dev" build) is never
// offered an update.
func Newer(latest, current string) bool {
	l, ok1 := parseSemver(strings.TrimPrefix(latest, "v"))
	c, ok2 := parseSemver(strings.TrimPrefix(current, "v"))
	if !ok1 || !ok2 {
		return false
	}
	for i := range 3 {
		if l.core[i] != c.core[i] {
			return l.core[i] > c.core[i]
		}
	}
	return comparePre(l.pre, c.pre) > 0
}

type semver struct {
	core [3]int
	pre  string
}

// parseSemver reads MAJOR.MINOR.PATCH[-PRE][+BUILD].
func parseSemver(s string) (semver, bool) {
	var v semver
	s, _, _ = strings.Cut(s, "+")
	s, v.pre, _ = strings.Cut(s, "-")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v.core[i] = n
	}
	return v, true
}

// comparePre orders pre-release labels as semver does: no label is newer
// than any label, numeric parts compare as numbers (rc.10 > rc.9).
func comparePre(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aerr := strconv.Atoi(as[i])
		bn, berr := strconv.Atoi(bs[i])
		switch {
		case aerr == nil && berr == nil:
			if an != bn {
				return cmpInt(an, bn)
			}
		case aerr == nil:
			return -1 // numbers sort before words
		case berr == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	return cmpInt(len(as), len(bs))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
