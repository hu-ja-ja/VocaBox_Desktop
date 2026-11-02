package lyrics

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// The official lyrics API is the one the VocaColle app uses: nvapi with the
// app's frontend ID and version, and the signed-in session's cookies. The
// headers are the app's own; the shared HTTP client carries the cookie jar
// and the user agent.
const (
	nicoFrontendID      = "75"
	nicoFrontendVersion = "7.46.0"
	// maxLyricsBody bounds one lyrics response.
	maxLyricsBody = 8 << 20
)

// nicoProvider is one source: niconico's official lyrics, registered by a
// work's uploader. It needs the signed-in session, which the shared client
// sends from its jar.
type nicoProvider struct {
	client    *http.Client
	apiBase   string
	userAgent string
	loggedIn  func() bool
}

// NewNicoProvider returns niconico's official lyrics source. client is the
// app's HTTP client with its cookie jar; apiBase is nvapi's base URL;
// loggedIn reports whether a session is loaded, so a guest is told to sign
// in rather than sent to an API that answers 401.
func NewNicoProvider(client *http.Client, apiBase, userAgent string, loggedIn func() bool) Provider {
	return &nicoProvider{
		client:    client,
		apiBase:   strings.TrimRight(apiBase, "/"),
		userAgent: userAgent,
		loggedIn:  loggedIn,
	}
}

func (p *nicoProvider) ID() string { return SourceNiconico }

func (p *nicoProvider) Name() string { return "ニコニコ" }

func (p *nicoProvider) Fetch(q Query) (Doc, error) {
	if q.HasLyricsKnown && !q.HasLyrics {
		return Doc{}, ErrNotFound
	}
	if p.loggedIn != nil && !p.loggedIn() {
		return Doc{}, ErrLogin
	}
	if q.VideoID == "" || p.client == nil {
		return Doc{}, ErrFailed
	}
	endpoint := p.apiBase + "/v1/videos/" + url.PathEscape(q.VideoID) + "/lyrics"
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return Doc{}, fmt.Errorf("%w: %v", ErrFailed, err)
	}
	req.Header.Set("X-Frontend-Id", nicoFrontendID)
	req.Header.Set("X-Frontend-Version", nicoFrontendVersion)
	req.Header.Set("Accept", "application/json")
	if p.userAgent != "" {
		req.Header.Set("User-Agent", p.userAgent)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return Doc{}, fmt.Errorf("%w: %v", ErrFailed, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLyricsBody+1))
	if err != nil || len(body) > maxLyricsBody {
		return Doc{}, ErrFailed
	}
	var parsed nicoLyricsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Doc{}, nicoFailure(resp.StatusCode, 0, "")
	}
	if parsed.Meta.ErrorCode != "" || (parsed.Meta.Status != 0 && parsed.Meta.Status != http.StatusOK) {
		return Doc{}, nicoFailure(resp.StatusCode, parsed.Meta.Status, parsed.Meta.ErrorCode)
	}
	if resp.StatusCode/100 != 2 {
		return Doc{}, nicoFailure(resp.StatusCode, parsed.Meta.Status, "")
	}
	lines := make([]Line, 0, len(parsed.Data.Lyrics))
	timed := false
	for _, block := range parsed.Data.Lyrics {
		if len(block.Lines) == 0 {
			continue
		}
		line := Line{Text: strings.Join(block.Lines, "\n")}
		if block.StartMS != nil {
			line.StartMS = *block.StartMS
		}
		if block.EndMS != nil {
			line.EndMS = *block.EndMS
		}
		if line.EndMS > line.StartMS {
			timed = true
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return Doc{}, ErrNotFound
	}
	if !parsed.Data.HasTimeInformation || !timed {
		for i := range lines {
			lines[i].StartMS, lines[i].EndMS = 0, 0
		}
		return Doc{Timed: false, Lines: lines}, nil
	}
	return Doc{Timed: true, Lines: lines}, nil
}

// nicoFailure maps the API's error code, then its meta status and HTTP
// status, to the package's errors.
func nicoFailure(httpStatus, metaStatus int, code string) error {
	switch code {
	case "UNAUTHORIZED":
		return ErrUnauthorized
	case "FORBIDDEN":
		return ErrForbidden
	case "LYRICS_ACCESS_LIMIT_EXCEEDED":
		return ErrLimit
	case "NOT_FOUND":
		return ErrNotFound
	}
	for _, status := range []int{metaStatus, httpStatus} {
		switch status {
		case http.StatusUnauthorized:
			return ErrUnauthorized
		case http.StatusForbidden:
			return ErrForbidden
		case http.StatusNotFound:
			return ErrNotFound
		}
	}
	return fmt.Errorf("%w: status %d %s", ErrFailed, metaStatus, code)
}

// nicoLyricsResponse is GET /v1/videos/{id}/lyrics.
type nicoLyricsResponse struct {
	Meta struct {
		Status    int    `json:"status"`
		ErrorCode string `json:"errorCode"`
	} `json:"meta"`
	Data struct {
		VideoID            string           `json:"videoId"`
		HasTimeInformation bool             `json:"hasTimeInformation"`
		Lyrics             []nicoLyricBlock `json:"lyrics"`
	} `json:"data"`
}

// nicoLyricBlock is one timed block. Lines can hold more than one line, which
// the block times as one unit.
type nicoLyricBlock struct {
	Lines   []string `json:"lines"`
	StartMS *float64 `json:"startMs"`
	EndMS   *float64 `json:"endMs"`
}
