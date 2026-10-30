package schedule

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func testSpec(t *testing.T) Spec {
	t.Helper()
	root := t.TempDir()
	return Spec{
		Archive:   filepath.Join(root, "My Archive"),
		Binary:    filepath.Join(root, "bin", "chitragit"),
		Frequency: Daily, Hour: 2, Minute: 5,
		Path: "/opt/homebrew/bin:/usr/bin",
	}
}

// fakeCrontab simulates `crontab -l` and `crontab -`.
type fakeCrontab struct {
	tab     string
	exists  bool
	readErr bool
	writes  int
}

func (f *fakeCrontab) run(_ context.Context, stdin []byte, name string, args ...string) ([]byte, []byte, error) {
	if name != "crontab" {
		return nil, nil, errors.New("unexpected " + name)
	}
	switch args[0] {
	case "-l":
		if f.readErr {
			return nil, []byte("crontab: permission denied"), errors.New("exit 1")
		}
		if !f.exists {
			return nil, []byte("no crontab for tester"), errors.New("exit 1")
		}
		return []byte(f.tab), nil, nil
	case "-":
		f.tab, f.exists = string(stdin), true
		f.writes++
		return nil, nil, nil
	}
	return nil, nil, errors.New("bad args")
}

func TestCronKeepsUserLines(t *testing.T) {
	ctx := context.Background()
	user := "MAILTO=me@example.com\n# my backups\n30 1 * * * /usr/local/bin/backup --all\n"
	f := &fakeCrontab{tab: user, exists: true}
	backups := t.TempDir()
	c := For(Env{GOOS: "linux", Run: f.run, BackupDir: backups})
	spec := testSpec(t)

	if err := c.Install(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(f.tab, user) {
		t.Fatalf("user lines changed:\n%s", f.tab)
	}
	if ok, _ := c.Installed(ctx, spec.ID()); !ok {
		t.Fatal("job not found after install")
	}
	// The previous crontab was saved before the change.
	entries, _ := os.ReadDir(backups)
	if len(entries) != 1 {
		t.Fatalf("want 1 backup, got %d", len(entries))
	}
	if b, _ := os.ReadFile(filepath.Join(backups, entries[0].Name())); string(b) != user {
		t.Fatalf("backup = %q", b)
	}

	// Installing again replaces the block instead of adding a second one.
	spec.Hour = 4
	if err := c.Install(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(f.tab, blockStart(spec.ID())); n != 1 {
		t.Fatalf("want one block, got %d:\n%s", n, f.tab)
	}
	if !strings.Contains(f.tab, "5 4 * * * ") {
		t.Fatalf("new time missing:\n%s", f.tab)
	}

	if err := c.Remove(ctx, spec.ID()); err != nil {
		t.Fatal(err)
	}
	if f.tab != user {
		t.Fatalf("remove should restore the user's crontab exactly:\n%q", f.tab)
	}
	writes := f.writes
	if err := c.Remove(ctx, spec.ID()); err != nil || f.writes != writes {
		t.Fatalf("removing twice should be a no-op: %v, writes %d -> %d", err, writes, f.writes)
	}
}

func TestCronEmptyAndUnreadable(t *testing.T) {
	ctx := context.Background()
	f := &fakeCrontab{}
	c := For(Env{GOOS: "linux", Run: f.run})
	spec := testSpec(t)
	if err := c.Install(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(f.tab, blockStart(spec.ID())) {
		t.Fatalf("tab = %q", f.tab)
	}

	// If the crontab cannot be read it must never be overwritten.
	bad := &fakeCrontab{tab: "keep me\n", exists: true, readErr: true}
	c = For(Env{GOOS: "linux", Run: bad.run})
	if err := c.Install(ctx, spec); err == nil {
		t.Fatal("install should fail when the crontab is unreadable")
	}
	if bad.writes != 0 || bad.tab != "keep me\n" {
		t.Fatal("unreadable crontab was overwritten")
	}
}

func TestCronLineQuoting(t *testing.T) {
	spec := testSpec(t)
	spec.Archive = "/data/it's 100%"
	spec.Frequency, spec.Weekday = Weekly, time.Sunday
	line := cronLine(spec)
	if !strings.HasPrefix(line, "5 2 * * 0 ") {
		t.Errorf("schedule fields wrong: %s", line)
	}
	if !strings.Contains(line, `'/data/it'\''s 100\%'`) {
		t.Errorf("archive not quoted/escaped: %s", line)
	}
	if !strings.Contains(line, "sync --headless --archive") {
		t.Errorf("args missing: %s", line)
	}
}

func TestCutBlockWithoutEnd(t *testing.T) {
	id := "chitragit-x"
	tab := "a\n" + blockStart(id) + "\nb\n"
	got, found := cutBlock(tab, id)
	if !found || got != "a\nb\n" {
		t.Fatalf("got %q %v", got, found)
	}
}

func TestLaunchd(t *testing.T) {
	ctx := context.Background()
	var calls []string
	run := func(_ context.Context, _ []byte, name string, args ...string) ([]byte, []byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil, nil, nil
	}
	home := t.TempDir()
	l := For(Env{GOOS: "darwin", Run: run, Home: home, UID: 501})
	spec := testSpec(t)
	spec.Archive += " & <co>"
	spec.Frequency, spec.Weekday = Weekly, time.Monday
	if err := l.Install(ctx, spec); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(home, "Library", "LaunchAgents", "tech.ttox."+spec.ID()+".plist")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"&amp; &lt;co&gt;", "<key>Weekday</key><integer>1</integer>",
		"<key>Hour</key><integer>2</integer>", "<string>--headless</string>", "/opt/homebrew/bin"} {
		if !strings.Contains(s, want) {
			t.Errorf("plist missing %q:\n%s", want, s)
		}
	}
	if len(calls) != 2 || !strings.HasPrefix(calls[1], "launchctl bootstrap gui/501 ") {
		t.Errorf("calls = %v", calls)
	}
	if ok, _ := l.Installed(ctx, spec.ID()); !ok {
		t.Error("not installed")
	}
	if err := l.Remove(ctx, spec.ID()); err != nil {
		t.Fatal(err)
	}
	if ok, _ := l.Installed(ctx, spec.ID()); ok {
		t.Error("still installed after remove")
	}
}

func TestSchtasks(t *testing.T) {
	ctx := context.Background()
	var xmlText string
	var created bool
	run := func(_ context.Context, _ []byte, name string, args ...string) ([]byte, []byte, error) {
		switch args[0] {
		case "/Create":
			b, err := os.ReadFile(args[4])
			if err != nil {
				return nil, nil, err
			}
			xmlText = decodeUTF16(t, b)
			created = true
		case "/Query":
			if !created {
				return nil, []byte("ERROR: not found"), errors.New("exit 1")
			}
		case "/Delete":
			created = false
		}
		return nil, nil, nil
	}
	s := For(Env{GOOS: "windows", Run: run, TempDir: t.TempDir()})
	spec := testSpec(t)
	spec.Frequency, spec.Weekday = Weekly, time.Friday
	if err := s.Install(ctx, spec); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<Friday />", "<StartWhenAvailable>true</StartWhenAvailable>",
		"T02:05:00</StartBoundary>", "sync --headless --archive"} {
		if !strings.Contains(xmlText, want) {
			t.Errorf("task XML missing %q:\n%s", want, xmlText)
		}
	}
	if ok, _ := s.Installed(ctx, spec.ID()); !ok {
		t.Error("not installed")
	}
	if err := s.Remove(ctx, spec.ID()); err != nil || created {
		t.Fatalf("remove: %v, still created %v", err, created)
	}
	if err := s.Remove(ctx, spec.ID()); err != nil {
		t.Fatalf("removing a missing task should be fine: %v", err)
	}
}

func TestTaskXMLWindowsPaths(t *testing.T) {
	spec := Spec{Archive: `C:\Users\Dev\R&D Repos`, Binary: `C:\Program Files\ChitraGit\chitragit.exe`,
		Frequency: Daily, Hour: 23, Minute: 30}
	x := string(taskXML(spec, time.Date(2026, 11, 1, 9, 0, 0, 0, time.Local)))
	for _, want := range []string{
		`<Command>C:\Program Files\ChitraGit\chitragit.exe</Command>`,
		`<Arguments>sync --headless --archive &#34;C:\Users\Dev\R&amp;D Repos&#34;</Arguments>`,
		"<StartBoundary>2026-11-01T23:30:00</StartBoundary>",
		"<DaysInterval>1</DaysInterval>",
	} {
		if !strings.Contains(x, want) {
			t.Errorf("missing %q:\n%s", want, x)
		}
	}
}

func TestWinQuote(t *testing.T) {
	cases := map[string]string{
		`plain`:         `plain`,
		`C:\a b\`:       `"C:\a b\\"`,
		`say "hi"`:      `"say \"hi\""`,
		``:              `""`,
		`C:\no\spaces\`: `C:\no\spaces\`,
	}
	for in, want := range cases {
		if got := winQuote(in); got != want {
			t.Errorf("winQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestNextAndParse(t *testing.T) {
	loc := time.FixedZone("IST", 5*3600+1800)
	now := time.Date(2026, 10, 29, 23, 0, 0, 0, loc) // a Thursday
	daily := Spec{Frequency: Daily, Hour: 2}
	if got := daily.Next(now); !got.Equal(time.Date(2026, 10, 30, 2, 0, 0, 0, loc)) {
		t.Errorf("daily next = %v", got)
	}
	weekly := Spec{Frequency: Weekly, Hour: 2, Weekday: time.Sunday}
	if got := weekly.Next(now); !got.Equal(time.Date(2026, 11, 1, 2, 0, 0, 0, loc)) {
		t.Errorf("weekly next = %v", got)
	}
	sameDay := Spec{Frequency: Weekly, Hour: 23, Minute: 30, Weekday: time.Thursday}
	if got := sameDay.Next(now); !got.Equal(time.Date(2026, 10, 29, 23, 30, 0, 0, loc)) {
		t.Errorf("same-day next = %v", got)
	}
	if h, m, err := ParseClock("2:30"); err != nil || h != 2 || m != 30 {
		t.Errorf("ParseClock = %d %d %v", h, m, err)
	}
	for _, bad := range []string{"24:00", "2", "02:7", "aa:bb", "12:60"} {
		if _, _, err := ParseClock(bad); err == nil {
			t.Errorf("ParseClock(%q) should fail", bad)
		}
	}
	if d, err := ParseWeekday("Mon"); err != nil || d != time.Monday {
		t.Errorf("ParseWeekday = %v %v", d, err)
	}
	if _, err := ParseWeekday("mo"); err == nil {
		t.Error("two letters should be ambiguous")
	}
}

func TestSpecValidateAndStore(t *testing.T) {
	spec := testSpec(t)
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := spec
	bad.Archive = "relative"
	bad.Hour = 25
	if err := bad.Validate(); err == nil {
		t.Error("bad spec should fail")
	}
	bad = spec
	bad.Archive += "\n* * * * * rm -rf ~"
	if err := bad.Validate(); err == nil {
		t.Error("line breaks must be refused")
	}

	meta := t.TempDir()
	if s, err := Load(meta); s != nil || err != nil {
		t.Fatalf("empty Load = %v %v", s, err)
	}
	if err := Save(meta, spec); err != nil {
		t.Fatal(err)
	}
	got, err := Load(meta)
	if err != nil || got == nil || *got != spec {
		t.Fatalf("Load = %+v %v", got, err)
	}
	if err := Forget(meta); err != nil {
		t.Fatal(err)
	}
	if err := Forget(meta); err != nil {
		t.Fatal(err)
	}
	if JobID("/a/b") != JobID("/a/b/") || JobID("/a/b") == JobID("/a/c") {
		t.Error("JobID should be stable per archive")
	}
}

func decodeUTF16(t *testing.T, b []byte) string {
	t.Helper()
	if len(b) < 2 || b[0] != 0xFF || b[1] != 0xFE {
		t.Fatal("missing UTF-16LE BOM")
	}
	u := make([]uint16, 0, len(b)/2)
	for i := 2; i+1 < len(b); i += 2 {
		u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
	}
	return string(utf16.Decode(u))
}
