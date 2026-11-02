package lyrics

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// maxCacheBody bounds one cached lyrics file; a track's lyrics are far
// smaller than a track's list.
const maxCacheBody = 1 << 20

// cacheVersion names the cache's data format. It is part of every file name,
// so bumping it starts the cache over rather than reading files written for
// another format. Bump it when the parsed document changes or a provider is
// added: the cache answers before the providers do.
const cacheVersion = "v1"

// cache keeps successful answers under one directory, one file per video.
// A file that cannot be read is a miss, not an error: the source is asked
// again.
type cache struct {
	dir string
}

// path returns where the video's lyrics are kept, reporting false for an ID
// that cannot name a file.
func (c *cache) path(videoID string) (string, bool) {
	if !validID(videoID) {
		return "", false
	}
	return filepath.Join(c.dir, cacheVersion+"-"+videoID+".json"), true
}

// load returns the cached lyrics of videoID, false when there is none or the
// file cannot be read.
func (c *cache) load(videoID string) (Doc, bool) {
	path, ok := c.path(videoID)
	if !ok {
		return Doc{}, false
	}
	body, err := os.ReadFile(path)
	if err != nil || len(body) > maxCacheBody {
		return Doc{}, false
	}
	var doc Doc
	if err := json.Unmarshal(body, &doc); err != nil || len(doc.Lines) == 0 {
		return Doc{}, false
	}
	return doc, true
}

// save writes the video's lyrics. Another process reading the file while it
// is written may see a torn body; the reader treats it as a miss and asks
// the source again, so the cache needs no temp file of its own.
// shortcut: a torn cache file costs one fetch, not correctness.
func (c *cache) save(videoID string, doc Doc) error {
	path, ok := c.path(videoID)
	if !ok {
		return errors.New("lyrics: video id cannot name a cache file")
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if len(body) > maxCacheBody {
		return errors.New("lyrics: cache body too large")
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}

// validID reports whether a string looks like a video ID: letters and digits
// only, as every niconico ID is, so a hand-edited file name can never point
// outside the cache directory.
func validID(videoID string) bool {
	if videoID == "" || len(videoID) > 32 {
		return false
	}
	for i := 0; i < len(videoID); i++ {
		c := videoID[i]
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			continue
		}
		return false
	}
	return true
}
