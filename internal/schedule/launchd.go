package schedule

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// launchd manages a per-user agent in ~/Library/LaunchAgents. Unlike cron,
// launchd runs a missed job when the Mac wakes up.
type launchd struct{ env Env }

func (l *launchd) Name() string { return "launchd" }

func launchdLabel(id string) string { return "tech.ttox." + id }

func (l *launchd) plistPath(id string) string {
	return filepath.Join(l.env.Home, "Library", "LaunchAgents", launchdLabel(id)+".plist")
}

func (l *launchd) domain() string { return fmt.Sprintf("gui/%d", l.env.UID) }

func (l *launchd) Install(ctx context.Context, spec Spec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	id := spec.ID()
	path := l.plistPath(id)
	if err := writeAtomic(path, plist(spec), 0o644); err != nil {
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	// Unload any older version first; it is fine if none was loaded.
	_, _, _ = l.env.Run(ctx, nil, "launchctl", "bootout", l.domain()+"/"+launchdLabel(id))
	if _, errOut, err := l.env.Run(ctx, nil, "launchctl", "bootstrap", l.domain(), path); err != nil {
		return schedulerError("launchd refused the job", errOut, err)
	}
	return nil
}

func (l *launchd) Installed(_ context.Context, id string) (bool, error) {
	_, err := os.Stat(l.plistPath(id))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (l *launchd) Remove(ctx context.Context, id string) error {
	_, _, _ = l.env.Run(ctx, nil, "launchctl", "bootout", l.domain()+"/"+launchdLabel(id))
	err := os.Remove(l.plistPath(id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func plist(spec Spec) []byte {
	var b bytes.Buffer
	str := func(s string) {
		b.WriteString("<string>")
		_ = xml.EscapeText(&b, []byte(s))
		b.WriteString("</string>")
	}
	key := func(k string) { b.WriteString("\n\t<key>" + k + "</key>\n\t") }

	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>`)
	key("Label")
	str(launchdLabel(spec.ID()))
	key("ProgramArguments")
	b.WriteString("<array>")
	for _, a := range append([]string{spec.Binary}, spec.Args()...) {
		b.WriteString("\n\t\t")
		str(a)
	}
	b.WriteString("\n\t</array>")
	if spec.Path != "" {
		key("EnvironmentVariables")
		b.WriteString("<dict>\n\t\t<key>PATH</key>\n\t\t")
		str(spec.Path)
		b.WriteString("\n\t</dict>")
	}
	key("StartCalendarInterval")
	b.WriteString(fmt.Sprintf("<dict>\n\t\t<key>Hour</key><integer>%d</integer>\n\t\t<key>Minute</key><integer>%d</integer>", spec.Hour, spec.Minute))
	if spec.Frequency == Weekly {
		b.WriteString(fmt.Sprintf("\n\t\t<key>Weekday</key><integer>%d</integer>", int(spec.Weekday)))
	}
	b.WriteString("\n\t</dict>")
	key("StandardOutPath")
	str("/dev/null")
	key("StandardErrorPath")
	str(filepath.Join(spec.Archive, ".chitragit", "logs", "schedule-errors.log"))
	key("ProcessType")
	str("Background")
	b.WriteString("\n</dict>\n</plist>\n")
	return b.Bytes()
}
