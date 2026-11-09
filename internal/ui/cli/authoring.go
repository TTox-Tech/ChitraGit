package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/i18n"
)

// authoringVerbs are git commands that write history. ChitraGit keeps the
// archive and never writes commits (PRD non-goal), and it never passes them
// through to git: that would run hooks and filters inside archived repos.
var authoringVerbs = []string{"commit", "add", "push", "merge", "rebase", "checkout", "switch", "reset", "cherry-pick", "stash"}

// authoringCmds answer those verbs with the repo's folder and the git
// command to run there, so a git habit ends in one copy-paste.
func (a *app) authoringCmds() []*cobra.Command {
	cmds := make([]*cobra.Command, 0, len(authoringVerbs))
	for _, verb := range authoringVerbs {
		cmds = append(cmds, &cobra.Command{
			Use:                verb,
			Hidden:             true,
			Args:               cobra.ArbitraryArgs,
			FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true}, // git's own flags, e.g. -m
			RunE: func(cmd *cobra.Command, args []string) error {
				hint := i18n.T("authoring.hint_any", "verb", verb)
				if dir := a.repoDirIn(cmd, args); dir != "" {
					hint = `git -C "` + dir + `" ` + verb // not %q: it would double Windows backslashes
				}
				return &core.Error{Category: core.CatConfig, Message: i18n.T("authoring.refuse", "verb", verb), Hint: hint}
			},
		})
	}
	return cmds
}

// repoDirIn returns the folder of the first argument that names a repo in
// the archive, or "" when none does or there is no archive here.
func (a *app) repoDirIn(cmd *cobra.Command, args []string) string {
	s, err := a.open(cmd.Context(), "", false)
	if err != nil {
		return ""
	}
	defer s.close()
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if _, dir, err := engine.FindRepo(cmd.Context(), s.arc.Root, arg, s.st); err == nil {
			return dir
		}
	}
	return ""
}
