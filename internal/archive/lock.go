package archive

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/platform"
)

// LockInfo is written into the lock file so other runs can tell who holds it.
type LockInfo struct {
	PID     int       `json:"pid"`
	Host    string    `json:"host"`
	Started time.Time `json:"started"`
	Command string    `json:"command"`
}

// Lock is a held archive lock. Release it when the run ends.
type Lock struct {
	path string
}

// LockedError is returned when another live process holds the lock.
type LockedError struct{ Holder LockInfo }

func (e *LockedError) Error() string {
	return fmt.Sprintf("archive is busy: %q has been running since %s (pid %d on %s)",
		e.Holder.Command, e.Holder.Started.Local().Format("15:04"), e.Holder.PID, e.Holder.Host)
}

// Acquire takes the archive lock. A lock left by a dead process on the same
// host is treated as stale and replaced.
func (a *Archive) Acquire(command string) (*Lock, error) {
	host, _ := os.Hostname()
	me := LockInfo{PID: os.Getpid(), Host: host, Started: time.Now(), Command: command}
	data, _ := json.Marshal(me)

	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(a.lockFile(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, werr := f.Write(data)
			cerr := f.Close()
			if werr != nil || cerr != nil {
				os.Remove(a.lockFile())
				return nil, errors.Join(werr, cerr)
			}
			return &Lock{path: a.lockFile()}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, &core.Error{Category: core.CatPermission, Message: "cannot create archive lock", Err: err}
		}

		holder, readErr := readLock(a.lockFile())
		if readErr == nil && !isStale(holder, host) {
			return nil, &LockedError{Holder: holder}
		}
		// Stale or unreadable (crash mid-write): remove and retry once.
		if err := os.Remove(a.lockFile()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, &core.Error{Category: core.CatPermission, Message: "cannot remove stale archive lock", Err: err}
		}
	}
	return nil, &core.Error{Category: core.CatInternal, Message: "could not acquire archive lock"}
}

// Release removes the lock file.
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	err := os.Remove(l.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func readLock(path string) (LockInfo, error) {
	var info LockInfo
	data, err := os.ReadFile(path)
	if err != nil {
		return info, err
	}
	err = json.Unmarshal(data, &info)
	return info, err
}

// staleAfter bounds how long a lock from another host is honoured, since we
// cannot check whether that process is still alive.
const staleAfter = 24 * time.Hour

func isStale(info LockInfo, host string) bool {
	if info.Host == host {
		return !platform.ProcessAlive(info.PID)
	}
	return time.Since(info.Started) > staleAfter
}
