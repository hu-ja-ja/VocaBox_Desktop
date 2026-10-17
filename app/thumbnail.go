package main

import (
	"bytes"
	"errors"
	"image"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hu-ja-ja/VocaBox_Desktop/locales"
	"golang.org/x/image/draw"
)

const (
	// thumbnailCDNHost is the host this app derives picture URLs from.
	thumbnailCDNHost = "nicovideo.cdn.nimg.jp"
	// thumbnailImageDomain is nicovideo's image domain: the only domain
	// pictures are ever fetched from, however the API names their host.
	thumbnailImageDomain = "nimg.jp"
	// maxThumbBody bounds a thumbnail image response and the disk cache file.
	maxThumbBody = 1 << 20
	// Thumbnail fetches are spaced and bounded: at most thumbConcurrency run
	// at once, each started at least thumbInterval after the last, and a 429
	// pauses all of them for thumbCooldown.
	thumbConcurrency = 2
	thumbInterval    = 100 * time.Millisecond
	thumbCooldown    = 30 * time.Second
	// maxThumbsInMemory is where the memory cache trims: past it, the pictures
	// no frame is drawing are dropped, and the ones a screen shows stay,
	// whatever their count. Pictures are halved as they are decoded
	// (decodeThumbnail), so a screenful of rows and the home's cards are the
	// room a cache needs.
	maxThumbsInMemory = 80
	// thumbDecodeHalfW is the width from which a picture is halved while it
	// is decoded: a .M picture is 320×180, and half of it still covers a
	// 45 DIP row at 200% scaling without scaling the picture up.
	thumbDecodeHalfW = 320
	// Picture sizes: a row and the player bar fix the height, and the width
	// follows each picture's own aspect ratio.
	thumbRowHeight = 45
	thumbBarHeight = 36
	// thumbSlotSlack is the room a slot keeps past the usual 16:9 picture, so a
	// picture a little wider than 16:9 does not move the title either.
	thumbSlotSlack = 8
)

// errThumbPaused reports the CDN rate limiting: the limiter has paused every
// fetch, and the caller should stop rather than try another URL.
var errThumbPaused = errors.New(locales.ErrNicoBusy())

// errThumbMissing reports the CDN has no picture at a URL, as an older video
// has no .M size: the caller may move on to the URL's smaller sibling.
var errThumbMissing = errors.New(locales.ErrThumbMissing())

// thumbnailLimiter bounds how fast pictures are fetched: at most the slot
// count run at once, each start at least interval after the last, and a
// cooldown after the CDN asked to slow down.
type thumbnailLimiter struct {
	slots    chan struct{}
	interval time.Duration
	cooldown time.Duration
	mu       sync.Mutex
	next     time.Time
	paused   time.Time
}

func newThumbnailLimiter(slots int, interval, cooldown time.Duration) *thumbnailLimiter {
	return &thumbnailLimiter{
		slots:    make(chan struct{}, slots),
		interval: interval,
		cooldown: cooldown,
	}
}

// acquire takes a slot and waits until this fetch's turn. release returns
// the slot once the fetch is done.
func (l *thumbnailLimiter) acquire() {
	l.slots <- struct{}{}
	l.mu.Lock()
	now := time.Now()
	start := l.next
	if start.Before(now) {
		start = now
	}
	if l.paused.After(start) {
		start = l.paused
	}
	l.next = start.Add(l.interval)
	l.mu.Unlock()
	if wait := time.Until(start); wait > 0 {
		time.Sleep(wait)
	}
}

func (l *thumbnailLimiter) release() { <-l.slots }

// pausedFor stops every fetch for d, as after a 429. A pause only ever
// lengthens.
func (l *thumbnailLimiter) pausedFor(d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if until := time.Now().Add(d); until.After(l.paused) {
		l.paused = until
	}
}

// Thumbnail returns the picture of the video at watchID: the disk cache
// when it has one, the picture at thumbURL fetched and cached otherwise.
// Empty thumbURL comes from entries saved before pictures were kept, where
// the CDN's own URL shape is derived from the watch ID. Pictures are only
// ever fetched from nicovideo's image domain.
func (m *Mylist) Thumbnail(watchID, thumbURL string) ([]byte, error) {
	cache, cacheOK := m.thumbCachePath(watchID)
	if cacheOK {
		if body, ok := readThumbFile(cache); ok {
			return body, nil
		}
	}
	var lastErr error
	for _, u := range m.thumbnailCandidates(watchID, thumbURL) {
		body, err := m.fetchThumbnail(u)
		if err == nil {
			if cacheOK {
				writeThumbFile(cache, body)
			}
			return body, nil
		}
		lastErr = err
		if !errors.Is(err, errThumbMissing) {
			// Only a picture the CDN genuinely does not have moves on to the
			// smaller sibling. A temporary failure, or a 429 that paused the
			// fetches, must leave no lesser picture cached in its place.
			break
		}
	}
	if lastErr == nil {
		lastErr = errors.New(locales.ErrThumbNone())
	}
	return nil, lastErr
}

// HasThumbnail reports whether the picture of watchID is already in the
// disk cache.
func (m *Mylist) HasThumbnail(watchID string) bool {
	path, ok := m.thumbCachePath(watchID)
	if !ok {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// thumbCachePath returns the disk cache path of watchID's picture, or
// ok=false for an ID of an unexpected shape: cache file names come from
// IDs, so only the simple shape CDN IDs have becomes a path.
func (m *Mylist) thumbCachePath(watchID string) (path string, ok bool) {
	if !isSafeID(watchID) {
		return "", false
	}
	path, err := m.configPath(filepath.Join("thumbs", watchID+".jpg"))
	if err != nil {
		return "", false
	}
	return path, true
}

func readThumbFile(path string) ([]byte, bool) {
	body, err := readBoundedFile(path, maxThumbBody)
	if err != nil || len(body) == 0 {
		return nil, false
	}
	return body, true
}

// writeThumbFile stores a picture through a temp file and rename, so a
// crash mid-write never leaves a torn image behind. Caching is best effort:
// a write that fails leaves the picture uncached, not the row empty.
func writeThumbFile(path string, body []byte) {
	_ = writeFileAtomic(path, body, 0o644, "")
}

// thumbnailCandidates returns the URLs to try for one video: the .M picture
// before the small one the API's URL names. The CDN asks for sizes by
// suffix, but its picture URLs are either extension-less or carry a version
// hash (…/{number}.{hash}), so the path decides: everything under the CDN's
// /thumbnails/ takes .M, and a URL of any other shape is used as it is (the
// deleted-video placeholder, for one).
func (m *Mylist) thumbnailCandidates(watchID, thumbURL string) []string {
	base := thumbURL
	if base == "" {
		if num := videoNumber(watchID); num != "" {
			base = "https://" + m.thumbHostName() + "/thumbnails/" + num + "/" + num
		}
	}
	if base == "" {
		return nil
	}
	if !m.isThumbnailCDNURL(base) {
		return []string{base}
	}
	return []string{base + ".M", base}
}

// isThumbnailCDNURL reports whether rawURL is one of the thumbnail CDN's
// /thumbnails/ pictures, whose .M and .L sizes are asked for by suffix.
func (m *Mylist) isThumbnailCDNURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host != m.thumbHostName() {
		return false
	}
	return strings.HasPrefix(u.Path, "/thumbnails/")
}

// thumbHostName is the host derived picture URLs are built from: the CDN,
// or the test server when a test set the field.
func (m *Mylist) thumbHostName() string {
	if m.thumbHost != "" {
		return m.thumbHost
	}
	return thumbnailCDNHost
}

// thumbHostAllowed reports whether a picture URL's host may be fetched:
// nicovideo's image domain, whose subdomains the API uses for pictures and
// for the deleted-video placeholder, or the test server when tests set the
// field. Every other host is never contacted.
func (m *Mylist) thumbHostAllowed(host string) bool {
	host = strings.ToLower(host)
	if m.thumbHost != "" {
		return host == strings.ToLower(m.thumbHost)
	}
	return host == thumbnailImageDomain || strings.HasSuffix(host, "."+thumbnailImageDomain)
}

func (m *Mylist) fetchThumbnail(rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || !m.thumbHostAllowed(u.Host) {
		return nil, errors.New(locales.ErrThumbBadHost())
	}
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New(locales.ErrThumbFetch())
	}
	req.Header.Set("User-Agent", mylistUserAgent)
	if m.thumbLimiter != nil {
		m.thumbLimiter.acquire()
		defer m.thumbLimiter.release()
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, errors.New(locales.ErrThumbNetwork())
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		if m.thumbLimiter != nil {
			m.thumbLimiter.pausedFor(m.thumbLimiter.cooldown)
		}
		return nil, errThumbPaused
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, errThumbMissing
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(locales.ErrThumbHTTP(resp.StatusCode))
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
		return nil, errors.New(locales.ErrThumbNotImage())
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxThumbBody+1))
	if err != nil || len(body) == 0 {
		return nil, errors.New(locales.ErrThumbFetch())
	}
	if len(body) > maxThumbBody {
		return nil, errors.New(locales.ErrThumbTooLarge())
	}
	return body, nil
}

// videoNumber returns the numeric part of a video ID like sm123 or nm456,
// which the CDN's thumbnail paths use; "" when the ID has no number.
func videoNumber(id string) string {
	i := 0
	for i < len(id) && (id[i] < '0' || id[i] > '9') {
		i++
	}
	j := i
	for j < len(id) && id[j] >= '0' && id[j] <= '9' {
		j++
	}
	if i == j {
		return ""
	}
	return id[i:j]
}

// isSafeID reports whether id has only letters, digits, hyphens and
// underscores: the shape CDN video IDs have, safe as a file name.
func isSafeID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// thumbnail shows one video's picture with a fixed height and a width that
// follows the picture's aspect ratio, inside a slot that keeps titles lined
// up however narrow the picture is.
func (app *nativeApp) thumbnail(c *ui.Context, item MylistItem, h float32) {
	bitmap := app.thumbAt(item.WatchID)
	width := thumbWidth(h, 0, 0)
	if bitmap != nil {
		w, hh := bitmap.Size()
		width = thumbWidth(h, w, hh)
	} else if item.Available {
		app.requestThumbnail(item)
	}
	ui.Row(c).Width(thumbSlotWidth(h, width)).Shrink(0).Children(func() {
		if bitmap != nil {
			ui.Image(c, bitmap).Size(width, h).Fit(ui.Cover).Radius(4).Shrink(0)
		} else {
			ui.Box(c).Size(width, h).Radius(4).Shrink(0).Background(app.theme.SurfaceHover)
		}
		ui.Spacer(c).Grow(1)
	})
}

// thumbWidth returns how wide a picture shows at height h: its own aspect
// ratio, 16:9 for a picture whose size is not known.
func thumbWidth(h float32, w, hh int) float32 {
	if w <= 0 || hh <= 0 {
		return h * 16 / 9
	}
	return h * float32(w) / float32(hh)
}

// thumbSlotWidth returns the width of the slot a picture of width w at
// height h sits in: the usual 16:9 width plus thumbSlotSlack, or the
// picture's own width when that is wider, so a wider picture takes the room
// it needs instead of being squeezed.
func thumbSlotWidth(h, w float32) float32 {
	return max(w, h*16/9+thumbSlotSlack)
}

// resetThumbnailFill replaces the background picture queue with the loaded
// mylist's entries, so an earlier mylist's leftover entries do not hold up
// the new one.
func (app *nativeApp) resetThumbnailFill(items []MylistItem) {
	app.fillQueue = append(app.fillQueue[:0], items...)
}

// queueThumbnailFill schedules the pictures of a mylist's next page to be
// cached in the background.
func (app *nativeApp) queueThumbnailFill(items []MylistItem) {
	app.fillQueue = append(app.fillQueue, items...)
}

// pumpThumbnailFill caches the next missing picture of the queued mylist, one
// load at a time, as the view builds. Keeping to one load keeps the shared
// limiter's queue short, so a row the user scrolls to is never stuck behind
// the whole mylist.
func (app *nativeApp) pumpThumbnailFill() {
	if app.window == nil || app.fillActive {
		return
	}
	for len(app.fillQueue) > 0 {
		item := app.fillQueue[0]
		app.fillQueue = app.fillQueue[1:]
		if !app.needsThumbnail(item) {
			continue
		}
		app.fillActive = true
		app.fillThumbnail(item)
		return
	}
}

// needsThumbnail reports whether item's picture still has to be cached.
func (app *nativeApp) needsThumbnail(item MylistItem) bool {
	return item.WatchID != "" && item.Available &&
		!app.thumbFail[item.WatchID] && !app.thumbBusy[item.WatchID] &&
		app.thumbs[item.WatchID] == nil && !app.mylist.HasThumbnail(item.WatchID)
}

// fillThumbnail caches one picture on disk without decoding it: a row that
// shows it later reads the disk cache.
func (app *nativeApp) fillThumbnail(item MylistItem) {
	app.thumbBusy[item.WatchID] = true
	watchID, thumbURL := item.WatchID, item.ThumbURL
	go func() {
		_, err := app.mylist.Thumbnail(watchID, thumbURL)
		app.window.Update(func() {
			app.fillActive = false
			delete(app.thumbBusy, watchID)
			if err != nil {
				app.thumbFail[watchID] = true
			}
		})
	}()
}

// decodeThumbnail decodes a picture for the memory cache, halving an
// oversized one as it does: the picture's own size is what the cache keeps,
// however small a row shows it, and half of the CDN's .M picture is all a
// row or the player bar draws. The CDN's generated pictures carry no EXIF
// orientation, so plain image.Decode serves.
func decodeThumbnail(body []byte) (*ui.Bitmap, error) {
	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if b := img.Bounds(); b.Dx() >= thumbDecodeHalfW {
		half := image.NewRGBA(image.Rect(0, 0, max(1, b.Dx()/2), max(1, b.Dy()/2)))
		draw.CatmullRom.Scale(half, half.Bounds(), img, b, draw.Src, nil)
		img = half
	}
	return ui.NewBitmap(img), nil
}

// thumbAt returns the picture the memory cache holds for watchID, marking
// it as one of the frame's: the cache trims the pictures no frame draws.
func (app *nativeApp) thumbAt(watchID string) *ui.Bitmap {
	bitmap := app.thumbs[watchID]
	if bitmap != nil {
		app.thumbSeen[watchID] = app.thumbFrame
	}
	return bitmap
}

// trimThumbs makes room once the memory cache is at its cap: the pictures
// the last frame drew stay, and only those no frame is drawing go. Dropping
// what a screen is showing would only make it decode them again.
func (app *nativeApp) trimThumbs() {
	if len(app.thumbs) < maxThumbsInMemory {
		return
	}
	for id := range app.thumbs {
		if app.thumbSeen[id] < app.thumbFrame-1 {
			delete(app.thumbs, id)
			delete(app.thumbSeen, id)
		}
	}
}

// requestThumbnail loads item's picture in the background: the memory
// cache, the disk cache, then the CDN. Loads of one video collapse into one
// goroutine, a failure is remembered until the mylist is loaded anew, and
// the picture is decoded there rather than on the view's thread, so a
// screenful arriving at once does not stall the frames behind the batch.
func (app *nativeApp) requestThumbnail(item MylistItem) {
	if app.window == nil || item.WatchID == "" || app.thumbBusy[item.WatchID] || app.thumbFail[item.WatchID] {
		return
	}
	app.trimThumbs()
	app.thumbBusy[item.WatchID] = true
	watchID, thumbURL := item.WatchID, item.ThumbURL
	go func() {
		body, err := app.mylist.Thumbnail(watchID, thumbURL)
		var bitmap *ui.Bitmap
		if err == nil {
			bitmap, err = decodeThumbnail(body)
		}
		app.window.Update(func() {
			delete(app.thumbBusy, watchID)
			if err != nil || !thumbSizeOK(bitmap) {
				app.thumbFail[watchID] = true
				return
			}
			app.thumbs[watchID] = bitmap
		})
	}()
}

// thumbSizeOK rejects absurd pictures, so neither a corrupt cache file nor
// a strange server response becomes a giant texture.
func thumbSizeOK(b *ui.Bitmap) bool {
	if b == nil {
		return false
	}
	w, h := b.Size()
	return w > 0 && h > 0 && w <= 2048 && h <= 2048
}

// resetThumbFailures lets pictures that once failed try again, after the
// mylist was loaded anew.
func (app *nativeApp) resetThumbFailures() {
	app.thumbFail = map[string]bool{}
}
