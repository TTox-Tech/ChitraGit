package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/i18n"
)

func (a *app) undoCmd() *cobra.Command {
	var run int64
	var dryRun, stash, yes bool
	cmd := &cobra.Command{
		Use:   "undo",
		Short: "Put back every ref the last sync moved",
		Long: "Undoes the last sync run (or --run N, IDs in `chitragit log`): every branch,\n" +
			"remote-tracking branch and tag it created, moved or deleted goes back to where it\n" +
			"was before. A ref that changed again since, such as a branch you committed to, is\n" +
			"kept as it is, and repos the run cloned stay. Each repo goes through `restore`, so\n" +
			"its refs are snapshotted first and the undo can itself be undone. Nothing is ever\n" +
			"deleted: the commits the sync brought stay in its snapshots. The checked-out\n" +
			"branch needs a clean working tree, or --stash.",
		Args: argsN(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx, "chitragit undo", !dryRun)
			if err != nil {
				return err
			}
			defer s.close()
			if s.git == nil { // a dry run reads git without taking the archive lock
				if s.git, _, err = engine.Git(ctx, 0); err != nil {
					return err
				}
			}
			if run == 0 {
				last, err := s.st.LastRun(ctx)
				if err != nil {
					return err
				}
				if last == nil {
					return &core.Error{Category: core.CatConfig, Message: "no sync has run yet, so there is nothing to undo"}
				}
				run = last.ID
			}
			plan, err := engine.PlanUndo(ctx, s.arc.Root, run, s.git, s.st)
			if err != nil {
				return err
			}
			moves := 0
			for _, u := range plan.Repos {
				moves += len(u.Moves)
			}
			if !a.jsonOut {
				a.printUndoPlan(plan, moves)
			}
			if dryRun || moves == 0 {
				if a.jsonOut {
					return a.printJSON(map[string]any{"schema": jsonSchema, "undo": plan, "undone": false})
				}
				return nil
			}
			if !yes {
				if a.jsonOut || !term.IsTerminal(int(os.Stdin.Fd())) {
					return &usageError{fmt.Errorf("not undone: pass --yes to undo without a prompt")}
				}
				fmt.Fprint(a.errOut, i18n.T("undo.confirm", "count", moves, "run", run))
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				if ans := strings.ToLower(strings.TrimSpace(line)); ans != "y" && ans != "yes" {
					return &usageError{fmt.Errorf("not undone")}
				}
			}
			failed := engine.Undo(ctx, s.arc.Root, plan, stash, s.git, s.st)
			if a.jsonOut {
				err = a.printJSON(map[string]any{"schema": jsonSchema, "undo": plan, "undone": true})
			} else {
				for _, u := range plan.Repos {
					switch {
					case u.Error != "":
						a.warn(u.Path + ": " + u.Error)
					case len(u.Moves) > 0:
						fmt.Fprintln(a.out, a.styles.OK.Render("✓ ")+i18n.T("undo.done", "path", u.Path, "count", len(u.Moves)))
						fmt.Fprintln(a.out, a.styles.Muted.Render("  "+i18n.T("restore.undo", "path", u.Path, "pre", u.Pre)))
					}
				}
			}
			if err == nil && failed > 0 {
				err = &silentError{code: core.ExitPartial}
			}
			return err
		},
	}
	fl := cmd.Flags()
	fl.Int64Var(&run, "run", 0, "undo this sync run instead of the last one")
	fl.BoolVar(&dryRun, "dry-run", false, "show what would change and change nothing")
	fl.BoolVar(&stash, "stash", false, "put uncommitted changes aside and back instead of refusing")
	fl.BoolVarP(&yes, "yes", "y", false, "undo without asking")
	return cmd
}

func (a *app) printUndoPlan(p *engine.UndoPlan, moves int) {
	st := a.styles
	for i := range p.Repos {
		u := &p.Repos[i]
		switch {
		case u.Error != "":
			a.warn(u.Path + ": " + u.Error)
			continue
		case len(u.Moves) > 0:
			a.printRestorePlan(&u.RestorePlan)
		case len(u.Kept) > 0:
			fmt.Fprintln(a.out, st.Title.Render(u.Path))
		}
		for _, ref := range u.Kept {
			fmt.Fprintf(a.out, "  %s  %s  %s\n", st.Muted.Render("keep  "), st.Text.Render(ref), st.Muted.Render(i18n.T("undo.kept")))
		}
	}
	if moves == 0 {
		fmt.Fprintln(a.out, st.Muted.Render(i18n.T("undo.nothing", "run", p.Run)))
	}
	for _, path := range p.Cloned {
		fmt.Fprintln(a.out, st.Muted.Render(i18n.T("undo.cloned", "path", path)))
	}
}
