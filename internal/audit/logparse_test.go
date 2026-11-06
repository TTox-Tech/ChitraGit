package audit

import (
	"strings"
	"testing"
)

func collect(t *testing.T, in string) ([]*fileChange, int) {
	t.Helper()
	var out []*fileChange
	n, err := parseLog(strings.NewReader(in), func(f *fileChange) error {
		out = append(out, f)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out, n
}

func TestParseLog(t *testing.T) {
	in := `commit aaa
author Dev One
date 2026-01-02T03:04:05+05:30

diff --git a/app.go b/app.go
index 1..2 100644
--- a/app.go
+++ b/app.go
@@ -3,0 +4,2 @@ func main() {
+key := "one"
+commit fake
@@ -10 +12 @@
-old
+diff --git a/evil b/evil
\ No newline at end of file
diff --git "a/sp ace\303\251.txt" "b/sp ace\303\251.txt"
new file mode 100644
--- /dev/null
+++ "b/sp ace\303\251.txt"
@@ -0,0 +1 @@
+hello
diff --git a/gone.txt b/gone.txt
deleted file mode 100644
--- a/gone.txt
+++ /dev/null
@@ -1 +0,0 @@
-bye
diff --git a/img.png b/img.png
Binary files a/img.png and b/img.png differ
commit bbb
author Dev Two
date 2026-01-01T00:00:00Z

diff --git a/empty b/empty
new file mode 100644
`
	files, commits := collect(t, in)
	if commits != 2 {
		t.Errorf("commits = %d", commits)
	}
	if len(files) != 5 {
		t.Fatalf("files = %d", len(files))
	}
	app := files[0]
	if app.path != "app.go" || app.commit != "aaa" || app.author != "Dev One" || app.date != "2026-01-02T03:04:05+05:30" {
		t.Errorf("app = %+v", app)
	}
	if len(app.hunks) != 2 || app.hunks[0].newStart != 4 || app.hunks[0].text != "key := \"one\"\ncommit fake\n" ||
		app.hunks[1].newStart != 12 || app.hunks[1].text != "diff --git a/evil b/evil\n" {
		t.Errorf("hunks = %+v", app.hunks)
	}
	if got := app.addedBytes(); got != int64(len(`key := "one"`)+len("commit fake")+len("diff --git a/evil b/evil")) {
		t.Errorf("addedBytes = %d", got)
	}
	if files[1].path != "sp aceé.txt" || files[1].hunks[0].text != "hello\n" {
		t.Errorf("quoted = %+v", files[1])
	}
	if files[2].path != "gone.txt" || !files[2].deleted {
		t.Errorf("deleted = %+v", files[2])
	}
	if !files[3].binary {
		t.Errorf("binary = %+v", files[3])
	}
	if files[4].path != "empty" || files[4].commit != "bbb" || files[4].author != "Dev Two" || len(files[4].hunks) != 0 {
		t.Errorf("empty = %+v", files[4])
	}
}

func TestParseLogMalformedHunkRecovers(t *testing.T) {
	in := "commit c1\nauthor A\ndate 2026-01-01T00:00:00Z\n\ndiff --git a/x b/x\n@@ -0,0 +1,5 @@\n+only one\ndiff --git a/y b/y\n@@ -0,0 +1 @@\n+y\n"
	files, _ := collect(t, in)
	if len(files) != 2 || files[1].path != "y" || files[1].hunks[0].text != "y\n" {
		t.Errorf("files = %+v", files)
	}
}

func TestParseHunkHeader(t *testing.T) {
	for _, c := range []struct {
		in             string
		start, old, nw int
		ok             bool
	}{
		{"@@ -1 +1 @@", 1, 1, 1, true},
		{"@@ -0,0 +1,3 @@ x", 1, 0, 3, true},
		{"@@ -5,2 +0,0 @@", 0, 2, 0, true},
		{"@@ nope @@", 0, 0, 0, false},
		{"@@ -a +1 @@", 0, 0, 0, false},
	} {
		s, o, n, ok := parseHunkHeader(c.in)
		if ok != c.ok || (ok && (s != c.start || o != c.old || n != c.nw)) {
			t.Errorf("%q = %d %d %d %v", c.in, s, o, n, ok)
		}
	}
}
