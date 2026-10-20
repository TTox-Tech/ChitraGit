package gitx

import (
	"strings"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// rule maps a fragment of git's (LC_ALL=C) stderr to a category, a
// plain-language message and a hint. Order matters: first match wins.
type rule struct {
	needle   string
	category core.Category
	message  string
	hint     string
}

var rules = []rule{
	{"detected dubious ownership", core.CatPermission,
		"git refuses to use this repo because it is owned by another user",
		"if you trust it, run: git config --global --add safe.directory <path>"},
	{"index.lock", core.CatUnsafeState,
		"another git process seems to be using this repo",
		"wait for it to finish; if none is running, remove .git/index.lock"},
	{"repository not found", core.CatNotFound, "the remote repository was not found", ""},
	{"does not appear to be a git repository", core.CatNotFound, "the remote repository was not found", ""},
	{"returned error: 404", core.CatNotFound, "the remote repository was not found", ""},
	{"returned error: 451", core.CatNotFound, "the remote repository is unavailable for legal reasons", ""},
	{"returned error: 429", core.CatRateLimited, "the server is rate limiting requests", "ChitraGit will retry later"},
	{"returned error: 403", core.CatAuth, "access to the remote repository was denied",
		"the repo may be private or your token lacks access"},
	{"authentication failed", core.CatAuth, "authentication failed",
		"check your credentials or run `chitragit auth login`"},
	{"could not read username", core.CatAuth, "the remote asked for a username and password",
		"the repo may be private or deleted; set up credentials or SSH keys"},
	{"permission denied (publickey", core.CatAuth, "SSH key was rejected",
		"check that your SSH key is added to your account and loaded in ssh-agent"},
	{"host key verification failed", core.CatAuth, "the server's SSH host key is not trusted",
		"connect once manually with ssh to verify and accept the host key"},
	{"rate limit", core.CatRateLimited, "the server is rate limiting requests", "ChitraGit will retry later"},
	{"could not resolve host", core.CatNetwork, "could not reach the server (DNS failed)", "check your internet connection"},
	{"failed to connect", core.CatNetwork, "could not connect to the server", "check your internet connection or proxy"},
	{"connection timed out", core.CatNetwork, "the connection timed out", ""},
	{"connection reset", core.CatNetwork, "the connection was reset", ""},
	{"operation too slow", core.CatNetwork, "the transfer was too slow and was aborted", ""},
	{"early eof", core.CatNetwork, "the connection closed unexpectedly", ""},
	{"the remote end hung up", core.CatNetwork, "the connection closed unexpectedly", ""},
	{"ssl certificate problem", core.CatNetwork, "the server's certificate could not be verified", "check your proxy or certificate settings"},
	{"gnutls", core.CatNetwork, "a secure connection could not be established", "check your proxy or certificate settings"},
	{"schannel", core.CatNetwork, "a secure connection could not be established", "check your proxy or certificate settings"},
	{"no space left on device", core.CatDisk, "the disk is full", "free some space and run again"},
	{"permission denied", core.CatPermission, "permission denied", "check folder permissions"},
	{"not a git repository", core.CatCorrupt, "this folder is not a valid git repository", ""},
	{"bad object", core.CatCorrupt, "the repository is damaged (bad object)", "run `git fsck` in this repo"},
	{"loose object", core.CatCorrupt, "the repository is damaged (corrupt object)", "run `git fsck` in this repo"},
	{"would be overwritten", core.CatUnsafeState, "the update would overwrite local files",
		"commit, move or delete the files git lists, then sync again"},
	{"not possible to fast-forward", core.CatUnsafeState, "the branch cannot be fast-forwarded", ""},
	{"conflict", core.CatConflict, "changes conflict", ""},
}

func classify(args []string, stderr []byte, cause error) *core.Error {
	text := strings.TrimSpace(string(stderr))
	lower := strings.ToLower(text)
	for _, r := range rules {
		if strings.Contains(lower, r.needle) {
			return &core.Error{Category: r.category, Message: r.message, Detail: text, Hint: r.hint, Err: cause}
		}
	}
	return &core.Error{
		Category: core.CatInternal,
		Message:  commandLine(args) + " failed",
		Detail:   text,
		Err:      cause,
	}
}
