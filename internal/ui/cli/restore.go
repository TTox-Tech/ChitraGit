package cli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/i18n"
)

func (a *app) restoreCmd() *cobra.Command {
	var to string
	var dryRun, stash, yes bool
	cmd := &cobra.Command{
		Use:   "restore <repo> --to <snapshot>",
		Short: "Put a repo's branches and tags back as a snapshot recorded them",
		Long: "Moves, creates and deletes refs so the repo's branches, remote-tracking\n" +
			"branches and tags equal the snapshot (see `chitragit history <repo>` for IDs).\n" +
			"First the current refs are recorded as a new snapshot, so a restore can itself\n" +
			"be undone. All refs move in one transaction. The checked-out branch moves with\n" +
			"its files and needs a clean working tree; --stash puts uncommitted changes aside\n" +
			"and back, and puts everything back as it was if they clash.",
		Args: argsN(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(to, 10, 64)
			if err != nil || id <= 0 {
				return &usageError{fmt.Errorf("--to needs a snapshot ID, e.g. --to 42 (see `chitragit history %s`)", args[0])}
			}
			ctx := cmd.Context()
			s, err := a.open(ctx, "chitragit restore", !dryRun)
			if err != nil {
				return err
			}
			defer s.close()
			if s.git == nil { // a dry run reads git without taking the archive lock
				if s.git, _, err = engine.Git(ctx, 0); err != nil {
					return err
				}
			}
			r, err := s.st.FindRepo(ctx, args[0])
			if err != nil {
				return err
			}
			plan, err := engine.PlanRestore(ctx, s.arc.Root, r, id, s.git, s.st)
			if err != nil {
				return err
			}
			if plan.Moves == nil {
				plan.Moves = []engine.RefMove{}
			}
			if !a.jsonOut {
				a.printRestorePlan(plan)
			}
			if dryRun || len(plan.Moves) == 0 {
				if a.jsonOut {
					return a.printJSON(map[string]any{"schema": jsonSchema, "restore": plan, "restored": false})
				}
				return nil
			}
			if !yes {
				if a.jsonOut || !term.IsTerminal(int(os.Stdin.Fd())) {
					return &usageError{fmt.Errorf("not restored: pass --yes to restore without a prompt")}
				}
				fmt.Fprint(a.errOut, i18n.T("restore.confirm", "count", len(plan.Moves), "path", r.Path, "id", id))
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				if ans := strings.ToLower(strings.TrimSpace(line)); ans != "y" && ans != "yes" {
					return &usageError{fmt.Errorf("not restored")}
				}
			}
			if err := engine.Restore(ctx, s.arc.Root, r, plan, stash, s.git, s.st); err != nil {
				return err
			}
			if a.jsonOut {
				return a.printJSON(map[string]any{"schema": jsonSchema, "restore": plan, "restored": true})
			}
			fmt.Fprintln(a.out, a.styles.OK.Render("✓ ")+i18n.T("restore.done", "path", r.Path, "id", id, "count", len(plan.Moves)))
			fmt.Fprintln(a.out, a.styles.Muted.Render(i18n.T("restore.undo", "path", r.Path, "pre", plan.Pre)))
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&to, "to", "", "the snapshot ID to restore (required)")
	fl.StringVar(&to, "source", "", "same as --to, as in git restore --source")
	fl.BoolVar(&dryRun, "dry-run", false, "show what would change and change nothing")
	fl.BoolVar(&stash, "stash", false, "put uncommitted changes aside and back instead of refusing")
	fl.BoolVarP(&yes, "yes", "y", false, "restore without asking")
	return cmd
}

func (a *app) printRestorePlan(p *engine.RestorePlan) {
	st := a.styles
	if len(p.Moves) == 0 {
		fmt.Fprintln(a.out, st.Muted.Render(i18n.T("restore.same", "path", p.Path, "id", p.Snapshot)))
		return
	}
	fmt.Fprintln(a.out, st.Title.Render(i18n.T("restore.plan", "path", p.Path, "id", p.Snapshot)))
	short := func(sha string) string { return sha[:min(12, len(sha))] }
	for _, m := range p.Moves {
		switch {
		case m.From == "":
			fmt.Fprintf(a.out, "  %s  %s  %s\n", st.OK.Render("create"), st.Text.Render(m.Ref), st.Muted.Render(short(m.To)))
		case m.To == "":
			fmt.Fprintf(a.out, "  %s  %s  %s\n", st.Warn.Render("delete"), st.Text.Render(m.Ref), st.Muted.Render(i18n.T("restore.was", "sha", short(m.From))))
		default:
			fmt.Fprintf(a.out, "  %s  %s  %s\n", st.Accent.Render("move  "), st.Text.Render(m.Ref), st.Muted.Render(short(m.From)+" → "+short(m.To)))
		}
	}
}
