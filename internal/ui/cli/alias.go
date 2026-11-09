package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/spf13/cobra"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/i18n"
)

// shortNames are the names the binary answers to besides chitragit (CG-43).
var shortNames = []string{"chit", "cg", "git-chit"}

// executable is os.Executable; tests point it at a fake binary.
var executable = os.Executable

// aliasCmd adds and removes the short names as links next to this binary:
// a symlink on Linux, a hard link on Windows (no admin rights needed).
// A name another program already uses is never taken.
func (a *app) aliasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alias",
		Short: "Add or remove the short names chit, cg and git-chit",
		Long: "The short names are links to this same program, so `chit sync` is `chitragit sync`.\n" +
			"`git-chit` lets you type `git chit sync`. A name another program already uses is left alone.",
	}
	ls := &cobra.Command{
		Use:   "ls",
		Short: "List the short names that point at this program",
		Args:  argsN(0, 0),
		RunE: func(*cobra.Command, []string) error {
			self, dir, err := selfAndDir()
			if err != nil {
				return err
			}
			var have []string
			for _, n := range shortNames {
				if sameFile(linkPath(dir, n), self) {
					have = append(have, n)
				}
			}
			if a.jsonOut {
				if have == nil {
					have = []string{}
				}
				return a.printJSON(map[string]any{"schema": jsonSchema, "dir": dir, "aliases": have})
			}
			if len(have) == 0 {
				fmt.Fprintln(a.out, a.styles.Muted.Render(i18n.T("alias.none")))
			}
			for _, n := range have {
				fmt.Fprintln(a.out, n)
			}
			return nil
		},
	}
	add := &cobra.Command{
		Use:   "add <chit|cg|git-chit>",
		Short: "Add a short name",
		Args:  argsN(1, 1),
		RunE: func(_ *cobra.Command, args []string) error {
			name, self, dir, err := aliasArgs(args[0])
			if err != nil {
				return err
			}
			link := linkPath(dir, name)
			if sameFile(link, self) {
				fmt.Fprintln(a.out, a.styles.OK.Render(a.icon(iconOK))+i18n.T("alias.exists", "name", name))
				return nil
			}
			if p, err := exec.LookPath(name); err == nil && !sameFile(p, self) {
				return &core.Error{Category: core.CatConfig, Message: i18n.T("alias.taken", "name", name, "path", p)}
			}
			if _, err := os.Lstat(link); err == nil {
				return &core.Error{Category: core.CatConfig, Message: i18n.T("alias.taken", "name", name, "path", link)}
			}
			if runtime.GOOS == "windows" {
				err = os.Link(self, link)
			} else {
				err = os.Symlink(filepath.Base(self), link)
			}
			if err != nil {
				return &core.Error{Category: core.CatPermission, Message: i18n.T("alias.failed", "name", name), Detail: err.Error(),
					Hint: "run it with rights to " + dir + ", or link it yourself"}
			}
			fmt.Fprintln(a.out, a.styles.OK.Render(a.icon(iconOK))+i18n.T("alias.added", "name", name, "path", link))
			return nil
		},
	}
	rm := &cobra.Command{
		Use:   "rm <chit|cg|git-chit>",
		Short: "Remove a short name (only if it points at this program)",
		Args:  argsN(1, 1),
		RunE: func(_ *cobra.Command, args []string) error {
			name, self, dir, err := aliasArgs(args[0])
			if err != nil {
				return err
			}
			link := linkPath(dir, name)
			if !sameFile(link, self) {
				return &core.Error{Category: core.CatConfig, Message: i18n.T("alias.not_ours", "path", link)}
			}
			if err := os.Remove(link); err != nil {
				return &core.Error{Category: core.CatPermission, Message: i18n.T("alias.failed", "name", name), Detail: err.Error()}
			}
			fmt.Fprintln(a.out, a.styles.OK.Render(a.icon(iconOK))+i18n.T("alias.removed", "name", name))
			return nil
		},
	}
	cmd.AddCommand(add, rm, ls)
	return cmd
}

func aliasArgs(name string) (string, string, string, error) {
	if !slices.Contains(shortNames, name) {
		return "", "", "", &usageError{fmt.Errorf("%q is not a short name; use chit, cg or git-chit", name)}
	}
	self, dir, err := selfAndDir()
	return name, self, dir, err
}

func selfAndDir() (string, string, error) {
	self, err := executable()
	if err == nil {
		self, err = filepath.EvalSymlinks(self)
	}
	if err != nil {
		return "", "", &core.Error{Category: core.CatEnvironment, Message: "cannot find this program's own file", Detail: err.Error()}
	}
	return self, filepath.Dir(self), nil
}

func linkPath(dir, name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name)
}

// sameFile reports whether path is (a link to) the file self.
func sameFile(path, self string) bool {
	a, err := os.Stat(path)
	if err != nil {
		return false
	}
	b, err := os.Stat(self)
	return err == nil && os.SameFile(a, b)
}
