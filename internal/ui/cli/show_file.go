package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/i18n"
	"github.com/TTox-Tech/chitragit/internal/ui/theme"
)

// maxShowFile is the largest file `show repo:path` prints; past it, use
// git in the repo itself.
const maxShowFile = 10 << 20

// showPath prints one file, folder, link or submodule of a repo.
func (a *app) showPath(ctx context.Context, o *objectArg, raw bool) error {
	entries, err := o.entries(ctx, false)
	if err != nil {
		return err
	}
	if o.path == "" || len(entries) != 1 || entries[0].Path != o.path {
		// A folder: list it, as git show does for a tree.
		if a.jsonOut {
			return a.printJSON(map[string]any{"schema": jsonSchema, "repo": o.repo.Path, "rev": o.rev, "path": o.path, "entries": entries})
		}
		for _, e := range entries {
			name := strings.TrimPrefix(e.Path, o.path+"/")
			if e.Type == "tree" {
				name = a.styles.Accent.Render(name + "/")
			}
			fmt.Fprintln(a.out, name)
		}
		return nil
	}
	e := entries[0]
	file := map[string]any{"schema": jsonSchema, "repo": o.repo.Path, "rev": o.rev, "path": e.Path, "mode": e.Mode, "sha": e.SHA, "size": e.Size}
	if e.Type == "commit" {
		if a.jsonOut {
			file["submodule"] = true
			return a.printJSON(file)
		}
		fmt.Fprintln(a.out, i18n.T("code.submodule", "path", e.Path, "sha", e.SHA))
		return nil
	}
	if e.Size > maxShowFile {
		return &core.Error{Category: core.CatConfig, Message: fmt.Sprintf("%s is %d bytes, too large to show", e.Path, e.Size),
			Hint: "read it with git in " + o.dir}
	}
	var data []byte
	binary := false
	if err := o.git.Blobs(ctx, o.dir, []string{e.SHA}, 0, func(b engine.GitBlob) error {
		data, binary = b.Data, b.Binary
		return nil
	}); err != nil {
		return err
	}
	link := e.Mode == "120000"
	if a.jsonOut {
		file["binary"], file["symlink"] = binary, link
		if !binary {
			file["text"] = strings.ToValidUTF8(string(data), "�")
		}
		return a.printJSON(file)
	}
	switch {
	case raw:
		_, err = a.out.Write(data)
		return err
	case link:
		fmt.Fprintln(a.out, i18n.T("code.symlink", "path", e.Path, "target", string(data)))
	case binary:
		fmt.Fprintln(a.out, i18n.T("code.binary", "path", e.Path, "size", e.Size))
	default:
		text := strings.ToValidUTF8(string(data), "�")
		fmt.Fprint(a.out, theme.Highlight(e.Path, text))
		if text != "" && !strings.HasSuffix(text, "\n") {
			fmt.Fprintln(a.out)
		}
	}
	return nil
}
