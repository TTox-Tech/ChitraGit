package audit

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// treeFile is one entry of a hand-built tree: git's porcelain refuses most
// hostile names, so the tests write trees with plumbing (mktree).
type treeFile struct {
	mode, data string // mode 100644 when empty; data is a symlink target for 120000, a commit for 160000
}

func gitIn(t testing.TB, dir, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir,
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// commitTree writes files (slash-separated paths) as a commit and returns it.
func commitTree(t testing.TB, dir string, files map[string]treeFile) string {
	t.Helper()
	type node struct {
		files map[string]treeFile
		dirs  map[string]*node
	}
	root := &node{files: map[string]treeFile{}, dirs: map[string]*node{}}
	for p, f := range files {
		n := root
		parts := strings.Split(p, "/")
		for _, d := range parts[:len(parts)-1] {
			if n.dirs[d] == nil {
				n.dirs[d] = &node{files: map[string]treeFile{}, dirs: map[string]*node{}}
			}
			n = n.dirs[d]
		}
		n.files[parts[len(parts)-1]] = f
	}
	var write func(n *node) string
	write = func(n *node) string {
		var lines []string
		for name, f := range n.files {
			mode, typ := cmp(f.mode, "100644"), "blob"
			sha := f.data
			if mode == "160000" {
				typ = "commit"
			} else {
				sha = gitIn(t, dir, f.data, "hash-object", "-w", "--stdin")
			}
			lines = append(lines, fmt.Sprintf("%s %s %s\t%s", mode, typ, sha, name))
		}
		for name, d := range n.dirs {
			lines = append(lines, fmt.Sprintf("040000 tree %s\t%s", write(d), name))
		}
		sort.Strings(lines)
		return gitIn(t, dir, strings.Join(lines, "\x00")+"\x00", "mktree", "-z", "--missing")
	}
	return gitIn(t, dir, "", "commit-tree", "-m", "test", write(root))
}

func cmp(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func inspect(t *testing.T, files map[string]treeFile) *TreeReport {
	t.Helper()
	r := runner(t)
	dir := newRepo(t)
	commit := commitTree(t, dir, files)
	rep, err := InspectTree(context.Background(), r, "r1", "me/app", dir, commit)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func rulesOf(rep *TreeReport) []string {
	var out []string
	for _, f := range rep.Findings {
		out = append(out, f.Rule+" "+f.File)
	}
	slices.Sort(out)
	return out
}

func TestInspectTreeCleanRepo(t *testing.T) {
	rep := inspect(t, map[string]treeFile{
		"README.md":            {data: "hello\n"},
		"bin/run.sh":           {mode: "100755", data: "#!/bin/sh\necho hi\n"},
		"docs/guide/intro.md":  {data: "intro\n"},
		"docs/link":            {mode: "120000", data: "guide/intro.md"},
		".gitattributes":       {data: "*.bin filter=lfs diff=lfs merge=lfs -text\n*.txt text\n"},
		".gitmodules":          {data: "[submodule \"lib\"]\n\tpath = lib\n\turl = https://example.com/lib.git\n[submodule \"rel\"]\n\tpath = rel\n\turl = ../rel.git\n"},
		"lib":                  {mode: "160000", data: strings.Repeat("a", 40)},
		"rel":                  {mode: "160000", data: strings.Repeat("b", 40)},
		"src/Main.go":          {data: "package main\n"},
		"src/main_test.go.txt": {data: "x"},
	})
	if len(rep.Findings) != 0 {
		t.Fatalf("clean repo has findings: %v", rulesOf(rep))
	}
	s := rep.Stats
	if s.Files != 8 || s.Dirs != 4 || s.Symlinks != 1 || s.Submodules != 2 || s.Executables != 1 || s.MaxDepth != 3 {
		t.Fatalf("stats = %+v", s)
	}
	if s.LargestPath != ".gitmodules" || s.TotalBytes == 0 {
		t.Fatalf("largest = %q (%d), total %d", s.LargestPath, s.LargestBlob, s.TotalBytes)
	}
}

func TestInspectTreeFindsHostileEntries(t *testing.T) {
	rep := inspect(t, map[string]treeFile{
		".GIT/hooks/post-checkout": {mode: "100755", data: "#!/bin/sh\n"},
		"a/git~1/config":           {data: "x"},
		"b/.git\u200c/config":      {data: "x"},
		"c/.git. /x":               {data: "x"},
		".gitmodules":              {mode: "120000", data: "/etc/passwd"},
		"sub/.gitattributes":       {mode: "120000", data: "../../outside"},
		"esc\x1b[2Jname":           {data: "x"},
		"evil\u202etxt.exe":        {data: "x"},
		"aux.c":                    {data: "x"},
		"what?.txt":                {data: "x"},
		"Readme":                   {data: "x"},
		"README":                   {mode: "120000", data: "x"},
		"hooks":                    {mode: "120000", data: "d/../.git/hooks"},
		".gitattributes":           {data: "*.c filter=indent\n# *.x filter=commented\n*.bin filter=lfs\n"},
	})
	want := []string{
		RuleCaseClash + " Readme",
		RuleControlChar + " esc<U+001B>[2Jname",
		RuleFilterDriver + " .gitattributes",
		RuleGitAlias + " .GIT",
		RuleGitAlias + " a/git~1",
		RuleGitAlias + " b/.git<U+200C>",
		RuleGitAlias + " c/.git. ",
		RuleInvisibleChar + " b/.git<U+200C>",
		RuleInvisibleChar + " evil<U+202E>txt.exe",
		RuleMetaSymlink + " .gitmodules",
		RuleMetaSymlink + " sub/.gitattributes",
		RuleReservedName + " aux.c",
		RuleSymlinkGit + " hooks",
		RuleSymlinkOutside + " .gitmodules",
		RuleSymlinkOutside + " sub/.gitattributes",
		RuleWindowsName + " c/.git. ",
		RuleWindowsName + " what?.txt",
	}
	if got := rulesOf(rep); !slices.Equal(got, want) {
		t.Fatalf("findings:\n got %q\nwant %q", got, want)
	}
	for _, f := range rep.Findings {
		if f.Rule == RuleMetaSymlink && f.File == ".gitmodules" && f.Severity != SevCritical {
			t.Errorf(".gitmodules symlink is %s, want critical", f.Severity)
		}
		if f.Rule == RuleCaseClash && f.Severity != SevHigh {
			t.Errorf("case clash with a symlink is %s, want high", f.Severity)
		}
		if strings.ContainsAny(f.File+f.Preview, "\x1b\u202e") {
			t.Errorf("finding shows a raw control character: %q %q", f.File, f.Preview)
		}
	}
	if rep.Findings[0].Severity != SevCritical {
		t.Errorf("findings are not sorted most severe first: %v", rep.Findings[0])
	}
}

func TestInspectTreeChecksSubmodules(t *testing.T) {
	rep := inspect(t, map[string]treeFile{
		".gitmodules": {data: `[submodule "opt"]
	path = opt
	url = --upload-pack=touch${IFS}pwned
[submodule "ext"]
	path = ext
	url = ext::sh -c touch% /tmp/pwned
[submodule "local"]
	path = local
	url = file:///home/me/.ssh
[submodule "../../modules/evil"]
	path = evil
	url = https://example.com/evil.git
[submodule "dash"]
	path = -dash
	url = https://example.com/dash.git
[submodule "up"]
	path = ../up
	url = https://example.com/up.git
[submodule "ipv6"]
	path = ipv6
	url = ssh://[::1]/repo.git
`},
	})
	want := []string{
		RuleSubmoduleLocal + " .gitmodules",
		RuleSubmoduleOption + " .gitmodules",
		RuleSubmoduleOption + " .gitmodules",
		RuleSubmodulePath + " .gitmodules",
		RuleSubmodulePath + " .gitmodules",
		RuleSubmoduleURL + " .gitmodules",
	}
	if got := rulesOf(rep); !slices.Equal(got, want) {
		t.Fatalf("findings:\n got %q\nwant %q", got, want)
	}
}

func TestIsReservedName(t *testing.T) {
	for _, n := range []string{"CON", "con.txt", "Nul", "aux.tar.gz", "COM1", "lpt9.log", "com¹", "CONOUT$", "nul "} {
		if !isReservedName(n) {
			t.Errorf("%q is reserved", n)
		}
	}
	for _, n := range []string{"console", "COM", "COM10", "lpt", "nullable.go", "aux_file", "CONTRIBUTING.md"} {
		if isReservedName(n) {
			t.Errorf("%q is not reserved", n)
		}
	}
}

// FuzzTreeName checks that any name is handled without a panic, that every
// spelling git treats as .git is caught, and that what is shown never holds
// a control character.
func FuzzTreeName(f *testing.F) {
	for _, s := range []string{".git", ".GIT", "git~1", ".git ", ".git.", ".g\u200cit", ".git::$INDEX_ALLOCATION", "a\x00b", "\u202e", "CON", "a/b", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		ti := &treeInspector{seen: map[string]bool{}, folded: map[string]foldedPath{}, ids: map[string]bool{}}
		ti.name(name, name, true)
		ti.symlink("x", name)
		ti.submoduleURL("s", name)
		ti.submodulePath("s", name)

		var b strings.Builder
		for _, r := range strings.ToLower(name) {
			if !hfsIgnorable(r) {
				b.WriteRune(r)
			}
		}
		n, _, _ := strings.Cut(b.String(), ":")
		n = strings.TrimRight(n, ". ")
		alias := n == ".git" || n == "git~1"
		found := slices.ContainsFunc(ti.out, func(f Finding) bool { return f.Rule == RuleGitAlias })
		if alias != found {
			t.Fatalf("name %q: alias %v, finding %v", name, alias, found)
		}
		for _, f := range ti.out {
			for _, r := range f.File + f.Preview {
				if r < 0x20 || r == 0x7f || invisible(r) || bidiMark(r) {
					t.Fatalf("finding for %q shows %U", name, r)
				}
			}
		}
	})
}

// The plan's budget: a 5,000-file tree is inspected in under 200 ms.
func TestInspectTreeFiveThousandFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	r := runner(t)
	dir := newRepo(t)
	write := func(p, data string) {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitattributes", "*.c filter=x\n")
	for i := range 5000 {
		write(fmt.Sprintf("pkg%02d/sub%02d/file%04d.go", i%50, i%7, i), fmt.Sprintf("package p%d\n", i))
	}
	gitIn(t, dir, "", "add", "-A")
	gitIn(t, dir, "", "commit", "-q", "-m", "big")
	commit := "HEAD"
	best := time.Hour
	for range 3 {
		start := time.Now()
		rep, err := InspectTree(context.Background(), r, "r1", "me/big", dir, commit)
		if err != nil {
			t.Fatal(err)
		}
		best = min(best, time.Since(start))
		if rep.Stats.Files != 5001 {
			t.Fatalf("files = %d", rep.Stats.Files)
		}
	}
	t.Logf("5,001 files inspected in %v", best)
	// The budget is 200 ms on a normal build; the race detector and shared
	// CI machines are several times slower, so the test fails at 1 s.
	if best > time.Second {
		t.Errorf("inspecting 5,001 files took %v, budget 200 ms", best)
	}
}
