package cli

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/TTox-Tech/chitragit/internal/config"
	"github.com/TTox-Tech/chitragit/internal/core"
)

// The docs site's command and settings reference pages are generated from the code,
// so they can never drift from what the binary does. After changing a command, flag
// or setting, refresh them with:
//
//	go test ./internal/ui/cli -run TestReferenceDocs -update-docs
var updateDocs = flag.Bool("update-docs", false, "rewrite website/docs/reference from the code")

const refDir = "../../../website/docs/reference"

func TestReferenceDocs(t *testing.T) {
	pages := map[string]string{
		"commands.md": commandsReference((&app{out: io.Discard, errOut: io.Discard}).rootCmd()),
		"settings.md": settingsReference(),
	}
	for name, want := range pages {
		path := filepath.Join(refDir, name)
		if *updateDocs {
			if err := os.MkdirAll(refDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run: go test ./internal/ui/cli -run TestReferenceDocs -update-docs)", path, err)
		}
		if strings.ReplaceAll(string(got), "\r\n", "\n") != want {
			t.Errorf("%s is out of date; run: go test ./internal/ui/cli -run TestReferenceDocs -update-docs", path)
		}
	}
}

func commandsReference(root *cobra.Command) string {
	var b bytes.Buffer
	b.WriteString("<!-- Generated from the code by TestReferenceDocs in internal/ui/cli. Do not edit. -->\n")
	b.WriteString("# Commands\n\n")
	b.WriteString("Every command works without the interactive ledger, takes `--json` for scripts and\n")
	b.WriteString("returns the [exit codes](#exit-codes) below. Run `chitragit <command> --help` for the same text.\n\n")
	b.WriteString("## Global flags\n\n")
	writeFlags(&b, root.PersistentFlags())

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			writeCommand(&b, sub)
			walk(sub)
		}
	}
	walk(root)

	b.WriteString("## Shell completion\n\n")
	b.WriteString("`chitragit completion bash|zsh|fish|powershell` prints a completion script. For example:\n\n")
	b.WriteString("```sh\nchitragit completion bash > ~/.local/share/bash-completion/completions/chitragit\n```\n\n")

	b.WriteString("## Exit codes\n\n| Code | Meaning |\n|---|---|\n")
	for _, e := range []struct {
		code int
		text string
	}{
		{core.ExitOK, "everything succeeded"},
		{core.ExitPartial, "finished, but some repos failed"},
		{core.ExitUsage, "bad flags, arguments or configuration"},
		{core.ExitLocked, "another run holds the archive lock"},
		{core.ExitEnvironment, "git is missing or too old, or the system is unusable"},
		{core.ExitInterrupted, "stopped by Ctrl+C"},
	} {
		fmt.Fprintf(&b, "| `%d` | %s |\n", e.code, e.text)
	}
	return b.String()
}

func writeCommand(b *bytes.Buffer, c *cobra.Command) {
	path := strings.TrimPrefix(c.CommandPath(), c.Root().Name()+" ")
	fmt.Fprintf(b, "## %s\n\n", path)
	if c.Short != "" {
		fmt.Fprintf(b, "%s.\n\n", strings.TrimSuffix(c.Short, "."))
	}
	if long := strings.TrimSpace(c.Long); long != "" && long != c.Short {
		fmt.Fprintf(b, "%s\n\n", longText(c.Long))
	}
	fmt.Fprintf(b, "```\n%s\n```\n\n", strings.TrimSpace(c.UseLine()))
	if len(c.Aliases) > 0 {
		fmt.Fprintf(b, "Also: `%s`.\n\n", strings.Join(c.Aliases, "`, `"))
	}
	if ex := strings.TrimSpace(c.Example); ex != "" {
		fmt.Fprintf(b, "Examples:\n\n```sh\n%s\n```\n\n", ex)
	}
	writeFlags(b, c.LocalNonPersistentFlags())
}

// longText turns a command's help text into Markdown: lines indented by two spaces
// (usage examples and tables in the help) become a code block.
func longText(long string) string {
	var out []string
	inCode, prevBlank := false, true
	for _, line := range strings.Split(strings.TrimSpace(long), "\n") {
		// An indented line starts a block only after a blank line or a heading
		// such as "Sources:"; otherwise it continues the paragraph above.
		indented := strings.HasPrefix(line, "  ") && (inCode || prevBlank)
		prevBlank = strings.TrimSpace(line) == "" || strings.HasSuffix(line, ":")
		switch {
		case indented && !inCode:
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			out = append(out, "```text")
			inCode = true
		case !indented && inCode && strings.TrimSpace(line) != "":
			out = append(trimBlank(out), "```", "")
			inCode = false
		}
		if inCode {
			line = strings.TrimPrefix(line, "  ")
		}
		out = append(out, line)
	}
	if inCode {
		out = append(trimBlank(out), "```")
	}
	return strings.Join(out, "\n")
}

func trimBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func writeFlags(b *bytes.Buffer, fs *pflag.FlagSet) {
	var rows []string
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Name == "help" {
			return
		}
		name := "--" + f.Name
		if f.Shorthand != "" {
			name = "-" + f.Shorthand + ", " + name
		}
		if t := f.Value.Type(); t != "bool" {
			name += " " + t
		}
		usage := strings.ReplaceAll(f.Usage, "|", "\\|")
		if d := f.DefValue; d != "" && d != "false" && d != "[]" && d != "0" && f.Value.Type() != "bool" {
			usage += fmt.Sprintf(" (default `%s`)", d)
		}
		rows = append(rows, fmt.Sprintf("| `%s` | %s |", name, usage))
	})
	if len(rows) == 0 {
		return
	}
	b.WriteString("| Flag | |\n|---|---|\n")
	b.WriteString(strings.Join(rows, "\n"))
	b.WriteString("\n\n")
}

func settingsReference() string {
	var b bytes.Buffer
	b.WriteString("<!-- Generated from the code by TestReferenceDocs in internal/ui/cli. Do not edit. -->\n")
	b.WriteString("# Settings\n\n")
	b.WriteString("Settings are layered: built-in defaults, then your personal `config.toml`, then the\n")
	b.WriteString("archive's `.chitragit/config.toml`, then `CHITRAGIT_*` environment variables, then flags.\n")
	b.WriteString("Change them with `chitragit config set <key> <value>` or on the Settings screen.\n\n")
	b.WriteString("Every key can also be set from the environment: `sync.workers` is `CHITRAGIT_SYNC_WORKERS`.\n")
	b.WriteString("Lists are comma separated; maps use `k=v,k2=v2`.\n\n")

	defaults := config.Defaults()
	bySection := map[string][]string{}
	var sections []string
	for _, k := range config.Keys() {
		sec := k[:strings.IndexByte(k, '.')]
		if _, ok := bySection[sec]; !ok {
			sections = append(sections, sec)
		}
		bySection[sec] = append(bySection[sec], k)
	}
	sort.Strings(sections)
	for _, sec := range sections {
		fmt.Fprintf(&b, "## [%s]\n\n| Key | Default | |\n|---|---|---|\n", sec)
		for _, k := range bySection[sec] {
			def, _ := defaults.Get(k)
			if def == "" {
				def = " "
			} else {
				def = "`" + strings.ReplaceAll(def, "|", "\\|") + "`"
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", k, def, strings.ReplaceAll(config.Doc(k), "|", "\\|"))
		}
		b.WriteString("\n")
	}
	return b.String()
}
