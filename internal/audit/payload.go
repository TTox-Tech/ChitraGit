package audit

import (
	"bytes"
	"path"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/gitx"
)

// CheckPayload covers files that are programs dressed up as something else
// (KodeKavach Shield threat T16): shortcuts, double extensions, and
// executables with a document's name.
const CheckPayload = "payload"

// Rules of the payload check.
const (
	RuleShortcutFile    = "payload-shortcut"
	RuleDoubleExt       = "payload-double-extension"
	RuleDisguisedExec   = "payload-disguised-executable"
	RuleCommittedBinary = "payload-binary"
)

// shortcutExts run or point somewhere when double-clicked and have no
// place in source code.
var shortcutExts = map[string]bool{
	".lnk": true, ".scr": true, ".pif": true, ".hta": true, ".desktop": true, ".url": true, ".scf": true,
	".vbe": true, ".jse": true, ".wsf": true, ".wsh": true, ".cpl": true, ".application": true,
	".appref-ms": true, ".settingcontent-ms": true, ".library-ms": true, ".searchconnector-ms": true,
}

// execExts start a program when opened on Windows or macOS.
var execExts = map[string]bool{
	".exe": true, ".scr": true, ".com": true, ".bat": true, ".cmd": true, ".pif": true, ".lnk": true,
	".vbs": true, ".vbe": true, ".js": true, ".jse": true, ".ps1": true, ".msi": true, ".jar": true,
	".hta": true, ".app": true, ".command": true,
}

// docExts are what a payload pretends to be.
var docExts = map[string]bool{
	".pdf": true, ".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true,
	".txt": true, ".rtf": true, ".csv": true, ".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".mp3": true, ".mp4": true, ".mov": true, ".zip": true, ".rar": true, ".7z": true, ".md": true,
}

// payloadCandidate reports the blobs whose first bytes the payload check
// reads: executable files, files with no extension, and files named like
// programs. Other files are judged by name alone, which keeps the check
// cheap on repos full of images and documents.
func payloadCandidate(e gitx.TreeEntry) bool {
	if !e.Regular() {
		return false
	}
	ext := strings.ToLower(path.Ext(e.Path))
	return e.Mode == "100755" || ext == "" || execExts[ext] || ext == ".dll" || ext == ".so" || ext == ".dylib"
}

// executableMagic names the program format data starts with, or "".
func executableMagic(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("MZ")):
		return "Windows program"
	case bytes.HasPrefix(data, []byte("\x7fELF")):
		return "Linux program"
	case bytes.HasPrefix(data, []byte{0xcf, 0xfa, 0xed, 0xfe}), bytes.HasPrefix(data, []byte{0xce, 0xfa, 0xed, 0xfe}),
		bytes.HasPrefix(data, []byte{0xfe, 0xed, 0xfa, 0xcf}), bytes.HasPrefix(data, []byte{0xfe, 0xed, 0xfa, 0xce}):
		return "macOS program"
	case bytes.HasPrefix(data, []byte{0xca, 0xfe, 0xba, 0xbe}) && len(data) > 7 && data[7] < 20:
		// Mach-O fat header: a small architecture count, where a Java class file has its version.
		return "macOS program"
	}
	return ""
}

type payloadScanner struct {
	repoID, repo, commit string
	ids                  map[string]bool
	out                  []Finding
}

func (s *payloadScanner) add(rule string, sev Severity, file, title, preview, hint string) {
	id := FindingID(s.repoID, CheckPayload, rule, file)
	if s.ids[id] {
		return
	}
	s.ids[id] = true
	s.out = append(s.out, Finding{
		ID: id, RepoID: s.repoID, RepoPath: s.repo, Check: CheckPayload, Rule: rule, Title: title,
		Severity: sev, Confidence: ConfidenceHigh, File: displayPath(file), Commit: s.commit,
		Preview: snippet(displayPath(preview), 80), Occurrences: 1, Hint: hint, Status: StatusOpen,
	})
}

// name judges a file by its name alone.
func (s *payloadScanner) name(e gitx.TreeEntry) {
	if e.Type != "blob" {
		return
	}
	base := strings.ToLower(path.Base(e.Path))
	ext := path.Ext(base)
	if shortcutExts[ext] {
		s.add(RuleShortcutFile, SevHigh, e.Path, "A shortcut or script file that runs when opened ("+ext+")",
			path.Base(e.Path), "Windows or a Linux desktop runs or follows this file on a double-click. Source code has no need for one.")
	}
	inner := strings.TrimRight(strings.TrimSuffix(base, ext), " .")
	if execExts[ext] && docExts[path.Ext(inner)] {
		s.add(RuleDoubleExt, SevHigh, e.Path, "A program named like a document ("+path.Ext(inner)+ext+")",
			path.Base(e.Path), "Explorer hides known extensions, so this shows as a document but runs as a program.")
	}
	if i := strings.Index(e.Path, ".app/Contents/MacOS/"); i > 0 {
		s.add(RuleCommittedBinary, SevLow, e.Path[:i+4], "A macOS app bundle", path.Base(e.Path[:i+4]),
			"A committed app runs with your rights when opened. Build it from source instead if you can.")
	}
}

// blob judges a candidate file by its first bytes.
func (s *payloadScanner) blob(e gitx.TreeEntry, data []byte) {
	kind := executableMagic(data)
	if kind == "" {
		return
	}
	ext := strings.ToLower(path.Ext(e.Path))
	if docExts[ext] {
		s.add(RuleDisguisedExec, SevHigh, e.Path, "A "+kind+" named like a document ("+ext+")",
			path.Base(e.Path), "The name says "+ext+" but the file is a program, and it is marked executable.")
		return
	}
	s.add(RuleCommittedBinary, SevLow, e.Path, "A committed "+kind, path.Base(e.Path),
		"A program in the repo runs with your rights. Prefer one built from source or from a release you can verify.")
}
