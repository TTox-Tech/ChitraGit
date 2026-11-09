package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Every shell's completion script is generated, and loaded by the shell
// when it is installed: bash 4+ and PowerShell on the CI runners, zsh and
// fish where present. On Windows only PowerShell is loaded, as "bash" there
// may be WSL or Git Bash with its own idea of paths.
func TestCompletionScripts(t *testing.T) {
	isolate(t)
	for shell, c := range map[string]struct {
		marker string
		load   func(file string) *exec.Cmd
	}{
		"bash": {"__start_chitragit", func(f string) *exec.Cmd {
			return exec.Command("bash", "--norc", "--noprofile", "-c", `[ "${BASH_VERSINFO[0]}" -ge 4 ] || exit 0; source "$1" && complete -p chitragit`, "bash", f)
		}},
		"zsh": {"#compdef chitragit", func(f string) *exec.Cmd {
			return exec.Command("zsh", "-f", "-c", `autoload -U compinit && compinit -u -D && source "$1" && whence -w _chitragit`, "zsh", f)
		}},
		"fish": {"complete -c chitragit", func(f string) *exec.Cmd {
			return exec.Command("fish", "--no-config", "-c", "source $argv[1]; and complete -c chitragit", f)
		}},
		"powershell": {"Register-ArgumentCompleter", func(f string) *exec.Cmd {
			return exec.Command("pwsh", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", ". '"+f+"'")
		}},
	} {
		t.Run(shell, func(t *testing.T) {
			code, out, errOut := run(t, "completion", shell)
			if code != 0 || !strings.Contains(out, c.marker) {
				t.Fatalf("completion %s: exit %d, %d bytes, %q", shell, code, len(out), errOut)
			}
			file := filepath.Join(t.TempDir(), "chitragit."+shell)
			if err := os.WriteFile(file, []byte(out), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := c.load(file)
			if runtime.GOOS == "windows" && shell != "powershell" {
				t.Skipf("%s is not loaded on Windows", shell)
			}
			if _, err := exec.LookPath(cmd.Path); err != nil {
				t.Skipf("%s not installed", shell)
			}
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("%s could not load the script: %v\n%s", shell, err, b)
			}
		})
	}
}

// The scripts ask the binary for candidates, so check its answers too.
func TestCompletionCandidates(t *testing.T) {
	isolate(t)
	for args, want := range map[string]string{
		"sy":           "sync",
		"audit fi":     "findings",
		"--js":         "--json",
		"completion p": "powershell",
	} {
		code, out, errOut := run(t, append([]string{"__complete"}, strings.Fields(args)...)...)
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("complete %q: exit %d, %q %q; want %s", args, code, out, errOut, want)
		}
	}
}
