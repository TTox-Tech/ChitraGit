package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/TTox-Tech/chitragit/internal/archive"
	"github.com/TTox-Tech/chitragit/internal/config"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/i18n"
	"github.com/TTox-Tech/chitragit/internal/platform"
	"github.com/TTox-Tech/chitragit/internal/version"
)

func (a *app) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Args:  argsN(0, 0),
		RunE: func(*cobra.Command, []string) error {
			if a.jsonOut {
				return a.printJSON(map[string]string{
					"version": version.Version, "commit": version.Commit, "date": version.Date,
					"os": runtime.GOOS, "arch": runtime.GOARCH, "go": runtime.Version(),
				})
			}
			fmt.Fprintln(a.out, version.String())
			return nil
		},
	}
}

func (a *app) initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [path]",
		Short: "Set up a folder as a ChitraGit archive",
		Long: "Creates a .chitragit folder in the archive root. It holds the archive's database,\n" +
			"settings, logs and backups, so the archive stays portable.",
		Args: argsN(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := "."
			if len(args) == 1 {
				root = args[0]
			}
			arc, created, err := archive.Init(cmd.Context(), root)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return a.printJSON(map[string]any{"path": arc.Root, "created": created})
			}
			if created {
				fmt.Fprintln(a.out, a.styles.OK.Render("✓ ")+a.styles.Text.Render(i18n.T("init.created", "path", arc.Root)))
			} else {
				fmt.Fprintln(a.out, a.styles.Muted.Render("· "+i18n.T("init.exists", "path", arc.Root)))
			}
			fmt.Fprintln(a.out, a.styles.Muted.Render("  "+i18n.T("init.next")))
			return nil
		},
	}
}

func (a *app) configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "View and change settings",
		Long: "Settings are layered, later wins:\n" +
			"  built-in defaults → user config → archive config → CHITRAGIT_* environment → flags\n\n" +
			"Keys are written as section.key, e.g. sync.workers.",
	}
	var userScope bool

	// target picks the file that `set` and `edit` write to.
	target := func() (string, error) {
		if !userScope {
			if arc, err := a.findArchive(); err == nil {
				return arc.ConfigFile(), nil
			}
		}
		return platform.UserConfigFile()
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "Show every setting with its effective value",
		Args:  argsN(0, 0),
		RunE: func(*cobra.Command, []string) error {
			cfg, warnings, err := a.loadConfigHere()
			if err != nil {
				return err
			}
			a.printWarnings(warnings)
			values := map[string]string{}
			for _, k := range config.Keys() {
				values[k], _ = cfg.Get(k)
			}
			if a.jsonOut {
				return a.printJSON(values)
			}
			for _, k := range config.Keys() {
				line := a.styles.Label.Render(k) + " = " + a.styles.Accent.Render(values[k])
				if doc := config.Doc(k); doc != "" {
					line += a.styles.Muted.Render("   # " + doc)
				}
				fmt.Fprintln(a.out, line)
			}
			return nil
		},
	}

	get := &cobra.Command{
		Use:   "get <key>",
		Short: "Print the effective value of a setting",
		Args:  argsN(1, 1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, warnings, err := a.loadConfigHere()
			if err != nil {
				return err
			}
			a.printWarnings(warnings)
			v, err := cfg.Get(args[0])
			if err != nil {
				return &core.Error{Category: core.CatConfig, Message: err.Error(), Hint: "run `chitragit config list` to see all keys"}
			}
			if a.jsonOut {
				return a.printJSON(map[string]string{"key": args[0], "value": v})
			}
			fmt.Fprintln(a.out, v)
			return nil
		},
	}

	set := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Change a setting (in the archive config, or --user)",
		Args:  argsN(2, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			file, err := target()
			if err != nil {
				return err
			}
			if err := config.SetInFile(file, args[0], args[1]); err != nil {
				return err
			}
			if a.jsonOut {
				return a.printJSON(map[string]string{"key": args[0], "value": args[1], "file": file})
			}
			fmt.Fprintln(a.out, a.styles.OK.Render("✓ ")+i18n.T("config.set_done", "key", args[0], "value", args[1], "file", file))
			return nil
		},
	}
	set.Flags().BoolVar(&userScope, "user", false, "write to your personal config instead of the archive's")

	path := &cobra.Command{
		Use:   "path",
		Short: "Show where config files live",
		Args:  argsN(0, 0),
		RunE: func(*cobra.Command, []string) error {
			paths := map[string]string{}
			if f, err := platform.UserConfigFile(); err == nil {
				paths["user"] = f
			}
			if arc, err := a.findArchive(); err == nil {
				paths["archive"] = arc.ConfigFile()
			}
			if a.jsonOut {
				return a.printJSON(paths)
			}
			for _, k := range []string{"user", "archive"} {
				if p, ok := paths[k]; ok {
					fmt.Fprintf(a.out, "%s %s\n", a.styles.Label.Render(fmt.Sprintf("%-8s", k)), p)
				}
			}
			return nil
		},
	}

	validate := &cobra.Command{
		Use:   "validate",
		Short: "Check config files for mistakes",
		Args:  argsN(0, 0),
		RunE: func(*cobra.Command, []string) error {
			_, warnings, err := a.loadConfigHere()
			if err != nil {
				return err
			}
			a.printWarnings(warnings)
			if a.jsonOut {
				return a.printJSON(map[string]any{"valid": true, "warnings": len(warnings)})
			}
			fmt.Fprintln(a.out, a.styles.OK.Render("✓ ")+i18n.T("config.valid"))
			return nil
		},
	}

	edit := &cobra.Command{
		Use:   "edit",
		Short: "Open the config file in your editor (archive config, or --user)",
		Args:  argsN(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			file, err := target()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
				return err
			}
			editor := findEditor()
			if editor == "" {
				fmt.Fprintln(a.out, i18n.T("config.no_editor"))
				fmt.Fprintln(a.out, "  "+file)
				return nil
			}
			fields := strings.Fields(editor)
			c := exec.CommandContext(cmd.Context(), fields[0], append(fields[1:], file)...)
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := c.Run(); err != nil {
				return err
			}
			_, warnings, err := a.loadConfigHere()
			a.printWarnings(warnings)
			return err
		},
	}
	edit.Flags().BoolVar(&userScope, "user", false, "edit your personal config instead of the archive's")

	cmd.AddCommand(list, get, set, path, validate, edit)
	return cmd
}

// loadConfigHere loads config using the current archive, if any.
func (a *app) loadConfigHere() (*config.Config, []config.Warning, error) {
	arc, err := a.findArchive()
	if err != nil {
		if a.archivePath != "" {
			return nil, nil, err
		}
		arc = nil
	}
	return a.loadConfig(arc)
}

func (a *app) printWarnings(ws []config.Warning) {
	for _, w := range ws {
		a.warn(w.String())
	}
}

func findEditor() string {
	for _, k := range []string{"VISUAL", "EDITOR"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	candidates := []string{"nano", "vim", "vi"}
	if runtime.GOOS == "windows" {
		candidates = []string{"notepad"}
	}
	for _, c := range candidates {
		if _, err := exec.LookPath(c); err == nil {
			return c
		}
	}
	return ""
}
