package conflict

import (
	"strings"
	"testing"
)

const diff3 = "top\n" +
	"<<<<<<< Updated upstream\n" +
	"theirs 1\n" +
	"||||||| Stash base\n" +
	"base 1\n" +
	"=======\n" +
	"mine 1\n" +
	">>>>>>> Stashed changes\n" +
	"middle\n" +
	"<<<<<<< Updated upstream\n" +
	"theirs 2\n" +
	"=======\n" +
	"mine 2\n" +
	">>>>>>> Stashed changes\n" +
	"end\n"

func TestParseAndResolve(t *testing.T) {
	segs, err := Parse([]byte(diff3))
	if err != nil {
		t.Fatal(err)
	}
	hs := Hunks(segs)
	if len(hs) != 2 {
		t.Fatalf("got %d hunks", len(hs))
	}
	if h := hs[0]; h.Line != 2 || h.Theirs != "theirs 1\n" || h.Base != "base 1\n" || h.Mine != "mine 1\n" {
		t.Errorf("hunk 1 = %+v", h)
	}
	if h := hs[1]; h.Line != 10 || h.Theirs != "theirs 2\n" || h.Base != "" || h.Mine != "mine 2\n" {
		t.Errorf("hunk 2 = %+v", h)
	}
	if string(Render(segs)) != diff3 {
		t.Error("render does not round-trip")
	}

	if !Resolve(segs, 1, Both) || Resolve(segs, 1, Mine) {
		t.Fatal("Resolve index handling is wrong")
	}
	got := string(Render(segs))
	if !strings.Contains(got, "middle\ntheirs 2\nmine 2\nend\n") || !strings.Contains(got, "<<<<<<< Updated upstream\ntheirs 1") {
		t.Errorf("after resolving hunk 2:\n%s", got)
	}
	Resolve(segs, 0, Mine)
	if got := string(Render(segs)); got != "top\nmine 1\nmiddle\ntheirs 2\nmine 2\nend\n" {
		t.Errorf("fully resolved:\n%q", got)
	}
}

func TestParseKeepsCRLFAndRejectsBrokenMarkers(t *testing.T) {
	crlf := strings.ReplaceAll(diff3, "\n", "\r\n")
	segs, err := Parse([]byte(crlf))
	if err != nil || len(Hunks(segs)) != 2 {
		t.Fatalf("CRLF: %v %d", err, len(Hunks(segs)))
	}
	Resolve(segs, 0, Theirs)
	if got := string(Render(segs)); !strings.HasPrefix(got, "top\r\ntheirs 1\r\nmiddle\r\n") {
		t.Errorf("CRLF lost: %q", got)
	}

	for _, bad := range []string{
		"<<<<<<< a\nx\n",                             // never closed
		"<<<<<<< a\nx\n=======\ny\n",                 // no end marker
		"<<<<<<< a\n<<<<<<< b\n=======\n>>>>>>> c\n", // nested
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	// Lines that only look like markers are text.
	ok := "<<<<<<<<< not a marker\n======== nor this\n>>>>>>>x\n"
	if segs, err := Parse([]byte(ok)); err != nil || len(Hunks(segs)) != 0 {
		t.Errorf("look-alike lines: %v %d", err, len(Hunks(segs)))
	}
}

func TestParseChoice(t *testing.T) {
	for in, want := range map[string]Choice{"mine": Mine, "THEIRS": Theirs, "both": Both} {
		if c, ok := ParseChoice(in); !ok || c != want {
			t.Errorf("%s = %v %v", in, c, ok)
		}
	}
	if _, ok := ParseChoice("ours"); ok {
		t.Error("accepted ours")
	}
}
