package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAliasAddLsRm(t *testing.T) {
	isolate(t)
	bin := t.TempDir()
	self := filepath.Join(bin, "chitragit")
	if runtime.GOOS == "windows" {
		self += ".exe"
	}
	if err := os.WriteFile(self, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer func(old func() (string, error)) { executable = old }(executable)
	executable = func() (string, error) { return self, nil }

	// A cg from another program is on PATH: it must be left alone.
	other := t.TempDir()
	if err := os.WriteFile(linkPath(other, "cg"), []byte("not us"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", other)

	if code, out, _ := run(t, "alias", "ls"); code != 0 || !strings.Contains(out, "No short names") {
		t.Errorf("empty ls = %d %q", code, out)
	}
	if code, _, errOut := run(t, "alias", "add", "chit"); code != 0 {
		t.Fatalf("add chit = %d %q", code, errOut)
	}
	if !sameFile(linkPath(bin, "chit"), self) {
		t.Fatal("chit does not point at the program")
	}
	if code, out, _ := run(t, "alias", "add", "chit"); code != 0 || !strings.Contains(out, "already points") {
		t.Errorf("second add = %d %q", code, out)
	}
	if code, _, errOut := run(t, "alias", "add", "cg"); code == 0 || !strings.Contains(errOut, "already used by") {
		t.Errorf("add cg over another program = %d %q", code, errOut)
	}
	if code, _, _ := run(t, "alias", "add", "cgit"); code == 0 {
		t.Error("cgit accepted")
	}
	if code, out, _ := run(t, "alias", "ls"); code != 0 || strings.TrimSpace(out) != "chit" {
		t.Errorf("ls = %d %q", code, out)
	}

	// rm never removes a file that is not our link.
	if err := os.WriteFile(linkPath(bin, "git-chit"), []byte("mine"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run(t, "alias", "rm", "git-chit"); code == 0 {
		t.Error("rm removed a foreign file")
	}
	if code, _, errOut := run(t, "alias", "rm", "chit"); code != 0 {
		t.Fatalf("rm chit = %d %q", code, errOut)
	}
	if _, err := os.Lstat(linkPath(bin, "chit")); !os.IsNotExist(err) {
		t.Errorf("chit still there: %v", err)
	}
	if _, err := os.Stat(self); err != nil {
		t.Errorf("the program itself is gone: %v", err)
	}
}
