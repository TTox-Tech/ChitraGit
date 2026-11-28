package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
)

// objectArg is a repo with a revision and maybe a path, written as in git:
// <repo>[@<rev>][:<path>].
type objectArg struct {
	repo    core.Repo
	dir     string
	rev     string // "HEAD" when none is given
	path    string
	hasPath bool
	git     *engine.GitRunner
}

// findObject reads a <repo>[@<rev>][:<path>] argument. A whole argument
// that names a repo wins, so repo names with "@" or ":" still work.
func findObject(ctx context.Context, s *session, arg string) (*objectArg, error) {
	o := &objectArg{rev: "HEAD"}
	r, err := s.st.FindRepo(ctx, arg)
	if err != nil {
		spec := arg
		if i := strings.Index(spec, ":"); i >= 0 {
			spec, o.path, o.hasPath = spec[:i], strings.Trim(arg[i+1:], "/"), true
		}
		if i := strings.LastIndex(spec, "@"); i >= 0 {
			spec, o.rev = spec[:i], spec[i+1:]
		}
		if spec == arg {
			return nil, err
		}
		if r, err = s.st.FindRepo(ctx, spec); err != nil {
			return nil, err
		}
		if o.rev == "" {
			return nil, &usageError{fmt.Errorf("%q: nothing after @; give a branch, tag or commit", arg)}
		}
	}
	if r.Missing {
		return nil, &core.Error{Category: core.CatConfig, Message: r.Path + " is missing from the archive",
			Hint: "run `chitragit scan` once it is back"}
	}
	o.repo, o.dir = r, engine.RepoDir(s.arc.Root, r)
	if o.git, err = readGit(ctx); err != nil {
		return nil, err
	}
	return o, nil
}

// entries lists the tree at o.path: the entry itself, or with list set and
// o.path a directory, what is in it.
func (o *objectArg) entries(ctx context.Context, recursive bool) ([]engine.TreeEntry, error) {
	p := o.path
	if p != "" {
		if e, err := o.git.Tree(ctx, o.dir, o.rev, engine.TreeOptions{Path: p}); err != nil {
			return nil, err
		} else if len(e) == 0 {
			return nil, &core.Error{Category: core.CatConfig, Message: fmt.Sprintf("no %s at %s in %s", p, o.rev, o.repo.Path)}
		} else if len(e) > 1 || e[0].Type != "tree" {
			return e, nil
		}
		p += "/"
	}
	return o.git.Tree(ctx, o.dir, o.rev, engine.TreeOptions{Path: p, TopLevel: !recursive})
}

// ---- ls-tree ----

func (a *app) lsTreeCmd() *cobra.Command {
	var recursive bool
	cmd := &cobra.Command{
		Use:   "ls-tree <repo>[@<rev>] [path]",
		Short: "List the files of a repo at a branch, tag or commit",
		Long: "Lists the files in a folder of a repo as committed, read from the archive and never from\n" +
			"a work tree. <rev> is a branch, tag or commit, HEAD when left out. A folder path lists\n" +
			"what is in it; -r lists everything below.",
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
			if len(args) == 2 {
				if o.hasPath {
					return &usageError{fmt.Errorf("give the path once, after : or as a second argument")}
				}
				o.path = strings.Trim(args[1], "/")
			}
			entries, err := o.entries(ctx, recursive)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return a.printJSON(map[string]any{"schema": jsonSchema, "repo": o.repo.Path, "rev": o.rev, "entries": entries})
			}
			st := a.styles
			for _, e := range entries {
				size, name := "-", e.Path
				if e.Size >= 0 {
					size = fmt.Sprint(e.Size)
				}
				if e.Type == "tree" {
					name = st.Accent.Render(name + "/")
				}
				fmt.Fprintf(a.out, "%s %s %s %8s\t%s\n", e.Mode, e.Type, st.Muted.Render(e.SHA), size, name)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "list every file below the folder")
	return cmd
}
