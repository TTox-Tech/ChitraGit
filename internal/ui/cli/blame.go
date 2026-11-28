package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/i18n"
)

func (a *app) blameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "blame <repo>[@<rev>] <path>",
		Short: "Show who last changed each line of a file",
		Long: "Shows, for each line of a file as committed at <rev> (HEAD when left out), the commit\n" +
			"that last changed it, following renames. The path can also follow a colon: repo@rev:path.",
		Args: argsN(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx, "", false)
			if err != nil {
				return err
			}
			defer s.close()
			o, err := findObject(ctx, s, args[0])
			if err != nil {
				return err
			}
			switch {
			case len(args) == 2 && o.hasPath:
				return &usageError{fmt.Errorf("give the path once, after : or as a second argument")}
			case len(args) == 2:
				o.path = strings.Trim(args[1], "/")
			case !o.hasPath || o.path == "":
				return &usageError{fmt.Errorf("which file? give a path: `chitragit blame <repo> <path>`")}
			}
			b, err := o.git.Blame(ctx, o.dir, o.rev, o.path, engine.BlameOptions{})
			if err != nil {
				return err
			}
			if a.jsonOut {
				return a.printJSON(map[string]any{"schema": jsonSchema, "repo": o.repo.Path, "rev": o.rev, "path": o.path, "blame": b})
			}
			a.printBlame(b)
			return nil
		},
	}
}

func (a *app) printBlame(b *engine.BlameResult) {
	st := a.styles
	width := 0
	for _, c := range b.Commits {
		width = max(width, min(len([]rune(c.Author)), 20))
	}
	digits := len(fmt.Sprint(len(b.Lines)))
	for _, l := range b.Lines {
		c := b.Commits[l.SHA]
		sha := l.SHA[:8]
		if c.Boundary {
			sha = "^" + l.SHA[:7]
		}
		author := []rune(c.Author)
		if len(author) > width {
			author = author[:width]
		}
		date := c.AuthorDate
		if len(date) >= 10 {
			date = date[:10]
		}
		fmt.Fprintf(a.out, "%s %s %s %s %s\n", st.Accent.Render(sha), st.Text.Render(fmt.Sprintf("%-*s", width, string(author))),
			st.Muted.Render(date), st.Muted.Render(fmt.Sprintf("%*d", digits, l.Line)), l.Text)
	}
	if b.Truncated {
		a.warn(i18n.T("code.blame_cut", "count", len(b.Lines)))
	}
}
