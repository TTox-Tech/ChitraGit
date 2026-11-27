package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/i18n"
)

// tagCmd names snapshots, the way git tag names a commit. Until CG-08 the
// word was an alias of label, so the old label verbs are turned away with
// a pointer instead of becoming snapshot names.
func (a *app) tagCmd() *cobra.Command {
	var list, del bool
	var only string
	cmd := &cobra.Command{
		Use:   "tag [<name> [repo…]]",
		Short: "Name the state of your repos now, or list and delete names",
		Long: "Gives every repo's branches and tags as they are now a name, such as pre-move,\n" +
			"that `restore --to` and `diff` accept in place of a snapshot ID. With repos or\n" +
			"--only it names just those. The name marks one snapshot per repo, taken now and\n" +
			"pinned against git gc; retention never removes a named snapshot. With no name,\n" +
			"or -l, lists the names; -d removes one (the snapshots stay). Your own repo\n" +
			"labels are `chitragit label`.",
		Args: argsN(0, 1<<20),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && (args[0] == "add" || args[0] == "rm" || args[0] == "ls") {
				return &usageError{fmt.Errorf("repo labels moved to `chitragit label %s`; tag now names snapshots", args[0])}
			}
			ctx := cmd.Context()
			switch {
			case del:
				if len(args) != 1 {
					return &usageError{fmt.Errorf("-d takes one name")}
				}
				return a.untag(cmd, args[0])
			case list || len(args) == 0:
				if len(args) > 0 || only != "" {
					return &usageError{fmt.Errorf("-l takes no name or repos")}
				}
				return a.listTags(cmd)
			}
			if err := engine.CheckSnapshotName(args[0]); err != nil {
				return err
			}
			s, err := a.open(ctx, "chitragit tag", true)
			if err != nil {
				return err
			}
			defer s.close()
			var repos []core.Repo
			if len(args) > 1 || only != "" {
				if repos, err = pickRepos(ctx, s.st, args[1:], only); err != nil {
					return err
				}
			} else {
				all, err := s.st.Repos(ctx)
				if err != nil {
					return err
				}
				for _, r := range all {
					if !r.Missing {
						repos = append(repos, r)
					}
				}
			}
			named, err := engine.NameSnapshots(ctx, s.arc.Root, args[0], repos, s.git, s.st)
			if err != nil {
				return err
			}
			failed := 0
			for _, n := range named {
				if n.Error != "" {
					failed++
				}
			}
			if a.jsonOut {
				err = a.printJSON(map[string]any{"schema": jsonSchema, "name": args[0], "snapshots": named})
			} else {
				for _, n := range named {
					if n.Error != "" {
						a.warn(n.Path + ": " + n.Error)
					}
				}
				fmt.Fprintln(a.out, a.styles.OK.Render("✓ ")+i18n.T("tag.done", "name", args[0], "count", len(named)-failed))
			}
			if err == nil && failed > 0 {
				err = &silentError{code: core.ExitPartial}
			}
			return err
		},
	}
	fl := cmd.Flags()
	fl.BoolVarP(&list, "list", "l", false, "list the names")
	fl.BoolVarP(&del, "delete", "d", false, "remove a name; its snapshots stay")
	fl.StringVar(&only, "only", "", "name only repos whose path matches this glob, e.g. 'work/*'")
	return cmd
}

func (a *app) listTags(cmd *cobra.Command) error {
	ctx := cmd.Context()
	s, err := a.open(ctx, "", false)
	if err != nil {
		return err
	}
	defer s.close()
	names, err := s.st.SnapshotNames(ctx)
	if err != nil {
		return err
	}
	if a.jsonOut {
		if names == nil {
			names = []engine.SnapshotName{}
		}
		return a.printJSON(map[string]any{"schema": jsonSchema, "names": names})
	}
	if len(names) == 0 {
		fmt.Fprintln(a.out, a.styles.Muted.Render(i18n.T("tag.none")))
		return nil
	}
	for _, n := range names {
		fmt.Fprintf(a.out, "%s  %s  %s\n", a.styles.Label.Render(n.Name), a.styles.Text.Render(localTime(n.At)),
			a.styles.Muted.Render(i18n.T("tag.repos", "count", n.Repos)))
	}
	return nil
}

func (a *app) untag(cmd *cobra.Command, name string) error {
	ctx := cmd.Context()
	s, err := a.open(ctx, "chitragit tag -d", true)
	if err != nil {
		return err
	}
	defer s.close()
	n, err := s.st.UnnameSnapshots(ctx, name)
	if err != nil {
		return err
	}
	if n == 0 {
		return &core.Error{Category: core.CatConfig, Message: fmt.Sprintf("no snapshot is named %q", name),
			Hint: "list the names with `chitragit tag -l`"}
	}
	if a.jsonOut {
		return a.printJSON(map[string]any{"schema": jsonSchema, "name": name, "removed": n})
	}
	fmt.Fprintln(a.out, a.styles.OK.Render("✓ ")+i18n.T("tag.deleted", "name", name, "count", n))
	return nil
}
