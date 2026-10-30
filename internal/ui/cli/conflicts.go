package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/TTox-Tech/chitragit/internal/conflict"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/i18n"
)

func (a *app) conflictsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "conflicts",
		Short: "List and resolve clashes between your changes and updates",
		Long: "When your uncommitted changes clash with an update, sync puts the repo back\n" +
			"exactly as it was and lists it here. Resolving applies the update again and\n" +
			"lays the clashes out in the files, for you to settle file by file or hunk by\n" +
			"hunk: keep mine, take theirs (upstream), or keep both.\n\n" +
			"  chitragit conflicts                         list repos with conflicts\n" +
			"  chitragit conflicts start <repo>            apply the update, show the clashes\n" +
			"  chitragit conflicts show <repo> [file]      files, or one file's hunks\n" +
			"  chitragit conflicts resolve <repo> <file> mine|theirs|both [--hunk N]\n" +
			"  chitragit conflicts edit <repo> <file>      fix it in your editor\n" +
			"  chitragit conflicts finish <repo>           keep the result as uncommitted changes\n" +
			"  chitragit conflicts abort <repo>            put the repo back exactly as it was\n" +
			"  chitragit conflicts dismiss <repo>          take it off the list, change nothing\n\n" +
			"Nothing is ever lost: your original changes are kept under\n" +
			"refs/chitragit/resolved/, and an aborted session's work under refs/chitragit/aborted/.",
		Args: argsN(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error { return a.listConflicts(cmd.Context()) },
	}
	var hunk int
	resolve := &cobra.Command{
		Use:   "resolve <repo> <file> <mine|theirs|both>",
		Short: "Settle a conflicted file, or one hunk of it with --hunk",
		Args:  argsN(3, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ok := conflict.ParseChoice(args[2])
			if !ok {
				return &usageError{fmt.Errorf("choose mine, theirs or both, not %q", args[2])}
			}
			return a.inSession(cmd.Context(), args[0], func(ctx context.Context, rc *repoConflict) error {
				var err error
				if hunk > 0 {
					err = rc.s.ResolveHunk(ctx, args[1], hunk-1, c)
				} else {
					err = rc.s.ResolveFile(ctx, args[1], c)
				}
				if err != nil {
					return err
				}
				return a.printFiles(ctx, rc)
			})
		},
	}
	resolve.Flags().IntVar(&hunk, "hunk", 0, "settle only this hunk (numbered as `conflicts show` lists them)")

	cmd.AddCommand(
		&cobra.Command{
			Use:   "start <repo>",
			Short: "Apply the waiting update and lay out the clashes",
			Args:  argsN(1, 1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return a.withRepo(cmd.Context(), args[0], func(ctx context.Context, rc *repoConflict) error {
					s, err := engine.StartResolve(ctx, rc.repo, rc.dir, rc.sess.git, rc.sess.st)
					if err != nil {
						return err
					}
					if s.Done {
						return a.done(rc, i18n.T("conflicts.nothing_clashed"))
					}
					rc.s = s
					return a.printFiles(ctx, rc)
				})
			},
		},
		&cobra.Command{
			Use:   "show <repo> [file]",
			Short: "List the conflicted files, or show one file's hunks",
			Args:  argsN(1, 2),
			RunE: func(cmd *cobra.Command, args []string) error {
				return a.inSession(cmd.Context(), args[0], func(ctx context.Context, rc *repoConflict) error {
					if len(args) == 1 {
						return a.printFiles(ctx, rc)
					}
					hs, err := rc.s.Hunks(ctx, args[1])
					if err != nil {
						return err
					}
					return a.printHunks(args[1], hs)
				})
			},
		},
		resolve,
		&cobra.Command{
			Use:   "edit <repo> <file>",
			Short: "Fix a conflicted file in your editor",
			Args:  argsN(2, 2),
			RunE: func(cmd *cobra.Command, args []string) error {
				return a.inSession(cmd.Context(), args[0], func(ctx context.Context, rc *repoConflict) error {
					if _, err := rc.s.Hunks(ctx, args[1]); err != nil {
						return err
					}
					editor := findEditor()
					if editor == "" {
						return &core.Error{Category: core.CatEnvironment, Message: i18n.T("config.no_editor"),
							Hint: "then run `chitragit conflicts done " + args[0] + " " + args[1] + "`"}
					}
					fields := strings.Fields(editor)
					c := exec.CommandContext(ctx, fields[0], append(fields[1:], filepath.Join(rc.dir, filepath.FromSlash(args[1])))...)
					c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
					if err := c.Run(); err != nil {
						return err
					}
					if err := rc.s.MarkResolved(ctx, args[1]); err != nil {
						return err
					}
					return a.printFiles(ctx, rc)
				})
			},
		},
		&cobra.Command{
			Use:   "done <repo> <file>",
			Short: "Mark a file resolved after fixing it by hand",
			Args:  argsN(2, 2),
			RunE: func(cmd *cobra.Command, args []string) error {
				return a.inSession(cmd.Context(), args[0], func(ctx context.Context, rc *repoConflict) error {
					if err := rc.s.MarkResolved(ctx, args[1]); err != nil {
						return err
					}
					return a.printFiles(ctx, rc)
				})
			},
		},
		&cobra.Command{
			Use:   "finish <repo>",
			Short: "End the session; keep the result as uncommitted changes",
			Args:  argsN(1, 1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return a.inSession(cmd.Context(), args[0], func(ctx context.Context, rc *repoConflict) error {
					if err := engine.FinishResolve(ctx, rc.repo, rc.s, rc.sess.st); err != nil {
						return err
					}
					return a.done(rc, i18n.T("conflicts.finished"))
				})
			},
		},
		&cobra.Command{
			Use:   "abort <repo>",
			Short: "Put the repo back exactly as it was before the session",
			Args:  argsN(1, 1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return a.withRepo(cmd.Context(), args[0], func(ctx context.Context, rc *repoConflict) error {
					ok, err := engine.AbortResolve(ctx, rc.repo, rc.dir, rc.sess.git, rc.sess.st)
					if err != nil {
						return err
					}
					if !ok {
						return a.done(rc, i18n.T("conflicts.nothing_to_abort"))
					}
					return a.done(rc, i18n.T("conflicts.aborted"))
				})
			},
		},
		&cobra.Command{
			Use:   "dismiss <repo>",
			Short: "Take a repo off the conflict list without changing it",
			Args:  argsN(1, 1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return a.withRepo(cmd.Context(), args[0], func(ctx context.Context, rc *repoConflict) error {
					if err := engine.DismissConflict(ctx, rc.repo, rc.dir, rc.sess.git, rc.sess.st); err != nil {
						return err
					}
					return a.done(rc, i18n.T("conflicts.dismissed"))
				})
			},
		},
	)
	return cmd
}

// repoConflict is one repo a conflicts command works on.
type repoConflict struct {
	sess *session
	repo core.Repo
	dir  string
	s    *conflict.Session
}

// withRepo takes the archive lock (sessions change working trees) and
// finds the repo.
func (a *app) withRepo(ctx context.Context, arg string, fn func(context.Context, *repoConflict) error) error {
	sess, err := a.open(ctx, "chitragit conflicts", true)
	if err != nil {
		return err
	}
	defer sess.close()
	repo, dir, err := engine.FindRepo(ctx, sess.arc.Root, arg, sess.st)
	if err != nil {
		return err
	}
	return fn(ctx, &repoConflict{sess: sess, repo: repo, dir: dir})
}

// inSession is withRepo for commands that need an open session.
func (a *app) inSession(ctx context.Context, arg string, fn func(context.Context, *repoConflict) error) error {
	return a.withRepo(ctx, arg, func(ctx context.Context, rc *repoConflict) error {
		s, err := engine.OpenResolve(ctx, rc.dir, rc.sess.git)
		if err != nil {
			return err
		}
		rc.s = s
		return fn(ctx, rc)
	})
}

func (a *app) listConflicts(ctx context.Context) error {
	s, err := a.open(ctx, "", false)
	if err != nil {
		return err
	}
	defer s.close()
	list, err := engine.Conflicts(ctx, s.st)
	if err != nil {
		return err
	}
	if a.jsonOut {
		return a.printJSON(map[string]any{"schema": jsonSchema, "conflicts": list})
	}
	if len(list) == 0 {
		fmt.Fprintln(a.out, a.styles.OK.Render("✓ ")+a.styles.Text.Render(i18n.T("conflicts.none")))
		return nil
	}
	for _, c := range list {
		files := strings.Join(c.Files, ", ")
		if files == "" {
			files = i18n.T("conflicts.files_unknown")
		}
		fmt.Fprintln(a.out, a.styles.Warn.Render(fmt.Sprintf("! %-10s ", c.Status))+a.styles.Label.Render(c.Repo)+"  "+
			a.styles.Muted.Render(files))
	}
	fmt.Fprintln(a.out)
	fmt.Fprintln(a.out, a.styles.Muted.Render(i18n.T("conflicts.next")))
	return nil
}

func (a *app) printFiles(ctx context.Context, rc *repoConflict) error {
	files, err := rc.s.Files(ctx)
	if err != nil {
		return err
	}
	if a.jsonOut {
		return a.printJSON(map[string]any{"schema": jsonSchema, "path": rc.repo.Path, "files": files})
	}
	open := 0
	for _, f := range files {
		if f.Resolved {
			fmt.Fprintln(a.out, a.styles.OK.Render("✓ ")+a.styles.Muted.Render(f.Path+"  "+i18n.T("conflicts.resolved")))
			continue
		}
		open++
		what := i18n.T("conflicts.kind." + f.Kind)
		if f.Hunks > 0 {
			what += " · " + i18n.T("conflicts.hunks", "count", f.Hunks)
		}
		fmt.Fprintln(a.out, a.styles.Warn.Render("✗ ")+a.styles.Label.Render(f.Path)+"  "+a.styles.Text.Render(what))
	}
	fmt.Fprintln(a.out)
	if open == 0 {
		fmt.Fprintln(a.out, a.styles.OK.Render(i18n.T("conflicts.all_resolved", "repo", rc.repo.Path)))
	} else {
		fmt.Fprintln(a.out, a.styles.Muted.Render(i18n.T("conflicts.howto", "repo", rc.repo.Path)))
	}
	return nil
}

func (a *app) printHunks(file string, hs []conflict.Hunk) error {
	if a.jsonOut {
		return a.printJSON(map[string]any{"schema": jsonSchema, "file": file, "hunks": hs})
	}
	if len(hs) == 0 {
		fmt.Fprintln(a.out, a.styles.Muted.Render(i18n.T("conflicts.no_hunks", "file", file)))
		return nil
	}
	side := func(label, text string) {
		fmt.Fprintln(a.out, "  "+a.styles.Accent.Render(label))
		if text == "" {
			fmt.Fprintln(a.out, a.styles.Muted.Render("    "+i18n.T("conflicts.empty_side")))
		}
		for _, l := range strings.SplitAfter(text, "\n") {
			if l != "" {
				fmt.Fprintln(a.out, a.styles.Text.Render("    │ "+strings.TrimRight(l, "\r\n")))
			}
		}
	}
	for i, h := range hs {
		fmt.Fprintln(a.out, a.styles.Title.Render(i18n.T("conflicts.hunk", "n", i+1, "line", h.Line)))
		side(i18n.T("conflicts.side_theirs"), h.Theirs)
		side(i18n.T("conflicts.side_mine"), h.Mine)
		fmt.Fprintln(a.out)
	}
	return nil
}

func (a *app) done(rc *repoConflict, msg string) error {
	if a.jsonOut {
		return a.printJSON(map[string]any{"schema": jsonSchema, "path": rc.repo.Path, "message": msg})
	}
	fmt.Fprintln(a.out, a.styles.OK.Render("✓ ")+a.styles.Label.Render(rc.repo.Path)+"  "+a.styles.Text.Render(msg))
	return nil
}
