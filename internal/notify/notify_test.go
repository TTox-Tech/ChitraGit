package notify

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

type recorder struct {
	sent []string
}

func (r *recorder) Send(_ context.Context, title, body string) error {
	r.sent = append(r.sent, title+"|"+body)
	return nil
}

func TestNotifierFilters(t *testing.T) {
	ctx := context.Background()
	r := &recorder{}
	n := Notifier{Desktop: true, On: []string{RunFinished}, Sender: r}
	_ = n.Notify(ctx, Message{Event: RunFinished, Title: "T", Body: "B"})
	_ = n.Notify(ctx, Message{Event: Rescued, Title: "x", Body: "y"})
	if len(r.sent) != 1 || r.sent[0] != "T|B" {
		t.Fatalf("sent = %v", r.sent)
	}
	n.Desktop = false
	_ = n.Notify(ctx, Message{Event: RunFinished})
	if len(r.sent) != 1 {
		t.Fatal("desktop=false must not send")
	}
}

func TestNativeCommands(t *testing.T) {
	title, body := "ChitraGit", `Synced "x"; $(rm -rf ~) & done`
	for _, goos := range []string{"linux", "darwin", "windows"} {
		var gotName string
		var gotArgs, gotEnv []string
		n := &native{goos: goos, lookPath: func(s string) (string, error) { return "/bin/" + s, nil },
			run: func(_ context.Context, env []string, name string, args ...string) error {
				gotName, gotArgs, gotEnv = name, args, env
				return nil
			}}
		if err := n.Send(context.Background(), title, body); err != nil {
			t.Fatalf("%s: %v", goos, err)
		}
		// The text must arrive as a whole argument or env value, never inside a script.
		switch goos {
		case "windows":
			if gotName != "powershell" || !slices.Contains(gotEnv, "CHITRAGIT_NOTIFY_BODY="+body) {
				t.Errorf("windows: %s %v", gotName, gotEnv)
			}
			if strings.Contains(strings.Join(gotArgs, " "), "rm -rf") {
				t.Error("windows: body leaked into the script")
			}
		default:
			if !slices.Contains(gotArgs, body) || !slices.Contains(gotArgs, title) {
				t.Errorf("%s: %s %v", goos, gotName, gotArgs)
			}
			for _, a := range gotArgs {
				if a != body && strings.Contains(a, "rm -rf") {
					t.Errorf("%s: body pasted into %q", goos, a)
				}
			}
		}
	}
}

func TestUnsupported(t *testing.T) {
	n := &native{goos: "linux", lookPath: func(string) (string, error) { return "", errors.New("nope") }}
	if err := n.Send(context.Background(), "a", "b"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
	n = &native{goos: "plan9"}
	if err := n.Send(context.Background(), "a", "b"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestGroup(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 5012: "5,012", 1234567: "1,234,567", -4500: "-4,500"} {
		if got := Group(n); got != want {
			t.Errorf("Group(%d) = %q, want %q", n, got, want)
		}
	}
}
