// Package notify shows desktop notifications (PRD F15) using each OS's
// own tool, so the binary needs no CGO: notify-send on Linux and BSD,
// osascript on macOS and a PowerShell toast on Windows.
//
// Notifications are a convenience. A failure to show one is returned to
// the caller for logging but must never fail a sync.
package notify

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Events a user can subscribe to with notify.on.
const (
	RunFinished = "run_finished"
	Rescued     = "rescued"
	Conflict    = "conflict"
)

// ErrUnsupported means this machine has no way to show a notification.
var ErrUnsupported = errors.New("desktop notifications are not available on this system")

// Message is one notification.
type Message struct {
	Event string
	Title string
	Body  string
}

// Sender shows a notification.
type Sender interface {
	Send(ctx context.Context, title, body string) error
}

// Notifier applies the user's notify settings before sending.
type Notifier struct {
	Desktop bool     // notify.desktop
	On      []string // notify.on
	Sender  Sender
}

// Notify sends m if its event is enabled. It gives up after a few seconds
// so a stuck notification daemon never delays the end of a run.
func (n Notifier) Notify(ctx context.Context, m Message) error {
	if !n.Desktop || n.Sender == nil || !slices.Contains(n.On, m.Event) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return n.Sender.Send(ctx, m.Title, m.Body)
}

// Runner runs a program with extra environment variables. Tests replace it.
type Runner func(ctx context.Context, env []string, name string, args ...string) error

func execRun(ctx context.Context, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) > 0 {
		return errors.New(strings.TrimSpace(string(out)))
	}
	return err
}

// Native returns the sender for this OS.
func Native() Sender {
	return &native{goos: runtime.GOOS, run: execRun, lookPath: exec.LookPath}
}

type native struct {
	goos     string
	run      Runner
	lookPath func(string) (string, error)
}

func (n *native) Send(ctx context.Context, title, body string) error {
	name, args, env := n.command(title, body)
	if name == "" {
		return ErrUnsupported
	}
	if _, err := n.lookPath(name); err != nil {
		return ErrUnsupported
	}
	return n.run(ctx, env, name, args...)
}

// command builds the program call. Title and body are passed as separate
// arguments or environment variables, never pasted into a script, so text
// such as a repo name cannot inject commands.
func (n *native) command(title, body string) (name string, args, env []string) {
	switch n.goos {
	case "darwin":
		return "osascript", []string{
			"-e", "on run argv",
			"-e", "display notification (item 2 of argv) with title (item 1 of argv)",
			"-e", "end run",
			title, body,
		}, nil
	case "windows":
		return "powershell", []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
				"-WindowStyle", "Hidden", "-Command", windowsToast},
			[]string{"CHITRAGIT_NOTIFY_TITLE=" + title, "CHITRAGIT_NOTIFY_BODY=" + body}
	case "linux", "freebsd", "openbsd", "netbsd", "dragonfly":
		return "notify-send", []string{"--app-name=ChitraGit", "--", title, body}, nil
	}
	return "", nil, nil
}

// windowsToast shows a toast through the Windows Runtime API, using
// PowerShell's own app ID so no installer registration is needed.
const windowsToast = `$ErrorActionPreference = 'Stop'
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null
$t = [System.Security.SecurityElement]::Escape($env:CHITRAGIT_NOTIFY_TITLE)
$b = [System.Security.SecurityElement]::Escape($env:CHITRAGIT_NOTIFY_BODY)
$x = New-Object Windows.Data.Xml.Dom.XmlDocument
$x.LoadXml("<toast><visual><binding template='ToastGeneric'><text>$t</text><text>$b</text></binding></visual></toast>")
$app = '{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe'
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($app).Show([Windows.UI.Notifications.ToastNotification]::new($x))`

// Group formats n with thousands separators: 5012 -> "5,012".
func Group(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
