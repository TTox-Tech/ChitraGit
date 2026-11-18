package gitx

import (
	"strings"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// Ref is one `<repo>[@rev][:path]` argument, as `show`, `ls-tree` and
// `blame` take it. Rev is "" when none was given (the repo's HEAD); Path is
// "" for the whole tree.
type Ref struct {
	Repo, Rev, Path string
}

// ParseRef splits a `<repo>[@rev][:path]` argument. The repo ends at the
// first '@' or ':' because archive paths never hold a ':' and repo names no
// '@'; the rev then ends at the next ':' so it may hold '@' ("main@{1}"),
// and the path keeps everything after it. The rev and path pass the same
// guards as Tree, so neither can reach git as an option or leave the repo.
func ParseRef(arg string) (Ref, error) {
	bad := func(msg string) (Ref, error) {
		return Ref{}, &core.Error{Category: core.CatConfig, Message: msg, Detail: arg,
			Hint: "use <repo>[@rev][:path], e.g. owner/repo@v1.2:docs/README.md"}
	}
	var ref Ref
	i := strings.IndexAny(arg, "@:")
	if i < 0 {
		ref.Repo = arg
	} else {
		ref.Repo, arg = arg[:i], arg[i:]
		if arg[0] == '@' {
			ref.Rev, ref.Path, _ = strings.Cut(arg[1:], ":")
			if checkRev(ref.Rev) != nil {
				return bad("not a revision")
			}
		} else {
			ref.Path = arg[1:]
		}
	}
	if ref.Repo == "" {
		return bad("no repo named")
	}
	// Git object paths use '/'; a Windows user may type '\'.
	ref.Path = strings.TrimSuffix(strings.ReplaceAll(ref.Path, "\\", "/"), "/")
	if ref.Path != "" && !safeTreePath(ref.Path) {
		return bad("not a path inside the repo")
	}
	return ref, nil
}
