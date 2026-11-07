package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSplitCommand(t *testing.T) {
	spaced := filepath.Join(t.TempDir(), "My Editor", "edit.exe")
	if err := os.MkdirAll(filepath.Dir(spaced), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spaced, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"vim", []string{"vim"}},
		{"code --wait", []string{"code", "--wait"}},
		{`"C:\Program Files\Notepad++\notepad++.exe" -multiInst -nosession`,
			[]string{`C:\Program Files\Notepad++\notepad++.exe`, "-multiInst", "-nosession"}},
		{`'/opt/my editor/bin/ed' -w`, []string{"/opt/my editor/bin/ed", "-w"}},
		{spaced, []string{spaced}},
		{"  ", nil},
		{`""`, []string{""}},
	} {
		if got := splitCommand(tc.in); !slices.Equal(got, tc.want) {
			t.Errorf("splitCommand(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
