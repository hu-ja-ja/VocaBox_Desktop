package lyrics

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
)

// maxCacheBody bounds one cached lyrics file; a track's lyrics are far
// smaller than a track's list.
const maxCacheBody = 1 << 20

// maxOffsetsBody bounds the one file keeping every lyrics timing shift.
// shortcut: every tuned track lives in the one file, so past 1 MiB its
// saves fail; split or prune the entries then.
const maxOffsetsBody = 1 << 20

// cacheVersion names the cache's data format. It is part of every file name,
// so bumping it starts the cache over rather than reading files written for
// another format. Bump it when the parsed document changes or the file
// naming changes; adding a provider writes files of its own.
const cacheVersion = "v2"

// cache keeps successful answers under one directory, one file per video and
// source. A file that cannot be read is a miss, not an error: the source is
// asked again.
type cache struct {
	dir string
	// offsets holds the timing shifts the user set, keyed by video and
	// source, each tied to the lyrics it was set on.
	offsets map[string]offsetEntry
}

// offsetEntry is one saved timing shift and the lyrics ID it belongs to, so
// a re-timed document starts over rather than keeping another's shift.
type offsetEntry struct {
	Lyrics string `json:"lyrics"`
	Offset int    `json:"offset"`
}

// path returns where the source's lyrics of the video are kept, reporting
// false for an ID that cannot name a file.
func (c *cache) path(videoID, sourceID string) (string, bool) {
	if !validID(videoID) || !validID(sourceID) {
		return "", false
	}
	return filepath.Join(c.dir, cacheVersion+"-"+videoID+"-"+sourceID+".json"), true
}

// load returns the cached lyrics of videoID from sourceID, false when there
// is none or the file cannot be read.
func (c *cache) load(videoID, sourceID string) (Doc, bool) {
	path, ok := c.path(videoID, sourceID)
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

// save writes the video's lyrics from a source. Another process reading the
// file while it is written may see a torn body; the reader treats it as a
// miss and asks the source again, so the cache needs no temp file of its own.
// shortcut: a torn cache file costs one fetch, not correctness.
func (c *cache) save(videoID, sourceID string, doc Doc) error {
	path, ok := c.path(videoID, sourceID)
	if !ok {
		return errors.New("lyrics: video or source id cannot name a cache file")
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

// offsetsPath is the one file keeping every lyrics timing shift, keyed by
// video and source rather than by the lyrics file, so a cache format change
// keeps the user's tuning.
func (c *cache) offsetsPath() string {
	return filepath.Join(c.dir, "offsets.json")
}

// offsetKey names one track's lyrics of a source in the offsets file: the
// entry keeps the lyrics ID in its value, so a re-timed document needs no
// second key.
func offsetKey(videoID, sourceID string) string {
	return videoID + "-" + sourceID
}

// loadOffsets returns the saved shifts. A missing file is empty, and one the
// app cannot read or parse is moved aside as offsets.json.bak first, so the
// next save starts over without destroying the shift it cannot read.
func (c *cache) loadOffsets() map[string]offsetEntry {
	offsets := map[string]offsetEntry{}
	path := c.offsetsPath()
	body, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			c.setAsideOffsets(path)
		}
		return offsets
	}
	if len(body) > maxOffsetsBody || json.Unmarshal(body, &offsets) != nil {
		c.setAsideOffsets(path)
		return map[string]offsetEntry{}
	}
	return offsets
}

// setAsideOffsets keeps a file the app cannot read next to it, so its shifts
// can be recovered by hand. A rename that fails leaves the file where it is,
// and the next save starts over as before.
func (c *cache) setAsideOffsets(path string) {
	if err := os.Rename(path, path+".bak"); err != nil {
		log.Printf("lyrics: keeping the offsets file aside: %v", err)
	}
}

// saveOffsets writes every shift through a temp file, as they are the user's
// own tuning. No shifts leaves no file: the default says nothing.
func (c *cache) saveOffsets(offsets map[string]offsetEntry) error {
	path := c.offsetsPath()
	if len(offsets) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	body, err := json.Marshal(offsets)
	if err != nil || len(body) > maxOffsetsBody {
		return errors.New("lyrics: offsets body too large")
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.dir, "offsets.*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}

// validID reports whether a string looks like a video or source ID: letters
// and digits only, so a hand-edited file name can never point outside the
// cache directory.
func validID(id string) bool {
	if id == "" || len(id) > 32 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			continue
		}
		return false
	}
	return true
}
