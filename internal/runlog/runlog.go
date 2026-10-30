// Package runlog writes one plain-text log file per headless run into
// <archive>/.chitragit/logs/ and removes old ones (PRD F15: rotated, kept
// 30 days by default).
package runlog

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Keep is how long run logs are kept.
const Keep = 30 * 24 * time.Hour

const stamp = "20060102-150405"

// names matches only files this package created, so Prune can never
// delete anything else a user keeps in the logs folder.
var names = regexp.MustCompile(`^([a-z]+)-(\d{8}-\d{6})(-\d+)?\.log$`)

// Create opens a new log file named <kind>-<time>.log.
func Create(dir, kind string, now time.Time) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	base := kind + "-" + now.Format(stamp)
	name := base + ".log"
	for i := 2; ; i++ {
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if !errors.Is(err, fs.ErrExist) || i > 100 {
			return f, err
		}
		name = base + "-" + strconv.Itoa(i) + ".log"
	}
}

// Prune deletes run logs older than keep and returns how many it removed.
func Prune(dir string, keep time.Duration, now time.Time) (int, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		m := names.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		t, err := time.ParseInLocation(stamp, m[2], now.Location())
		if err != nil || now.Sub(t) < keep {
			continue
		}
		if os.Remove(filepath.Join(dir, e.Name())) == nil {
			removed++
		}
	}
	return removed, nil
}

// Latest returns the newest log of the given kind, or "" if there is none.
func Latest(dir, kind string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var found []string
	for _, e := range entries {
		if m := names.FindStringSubmatch(e.Name()); m != nil && m[1] == kind {
			found = append(found, e.Name())
		}
	}
	if len(found) == 0 {
		return ""
	}
	// Same-second logs get a -2, -3… suffix; order by time, then suffix.
	key := func(name string) (string, int) {
		m := names.FindStringSubmatch(name)
		n, _ := strconv.Atoi(strings.TrimPrefix(m[3], "-"))
		return m[2], n
	}
	slices.SortFunc(found, func(a, b string) int {
		ta, na := key(a)
		tb, nb := key(b)
		if c := strings.Compare(ta, tb); c != 0 {
			return c
		}
		return na - nb
	})
	return filepath.Join(dir, found[len(found)-1])
}
