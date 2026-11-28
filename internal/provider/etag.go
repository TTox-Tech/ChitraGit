package provider

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"os"
	"path/filepath"
)

// ETags remembers API answers by URL, so the next request for the same URL
// can ask "has it changed?" with If-None-Match. An unchanged answer comes
// back as 304 Not Modified with no body, and GitHub does not count it
// against the rate limit. A store that fails only costs the saving.
type ETags interface {
	Load(key string) (Cached, bool)
	Save(key string, c Cached)
}

// Cached is one remembered answer.
type Cached struct {
	ETag string
	Link string // the Link header, for paging through a cached list
	Body []byte
}

// maxCached is the largest answer worth keeping.
const maxCached = 1 << 20

// etagKey keeps answers for different tokens apart: a token can see
// private repos that another token, or no token, cannot.
func etagKey(url, token string) string {
	sum := sha256.Sum256([]byte(url + "\x00" + token))
	return hex.EncodeToString(sum[:])
}

// DirETags keeps answers as one small file each in a folder, readable
// only by the user, since answers can describe private repos.
type DirETags string

// Load returns the answer saved for key.
func (d DirETags) Load(key string) (Cached, bool) {
	data, err := os.ReadFile(filepath.Join(string(d), key))
	if err != nil {
		return Cached{}, false
	}
	var c Cached
	if gob.NewDecoder(bytes.NewReader(data)).Decode(&c) != nil || c.ETag == "" {
		return Cached{}, false
	}
	return c, true
}

// Save writes the answer through a temporary file and a rename, so a
// reader never sees half a file.
func (d DirETags) Save(key string, c Cached) {
	var buf bytes.Buffer
	if gob.NewEncoder(&buf).Encode(c) != nil || os.MkdirAll(string(d), 0o700) != nil {
		return
	}
	tmp, err := os.CreateTemp(string(d), key+".*")
	if err != nil {
		return
	}
	_, err = tmp.Write(buf.Bytes())
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(string(d), key))
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
}
