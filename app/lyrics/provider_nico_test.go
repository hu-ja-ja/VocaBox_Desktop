package lyrics

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestNicoProviderReadsTimedLyrics(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/v1/videos/sm1/lyrics", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Frontend-Id"); got != nicoFrontendID {
			t.Errorf("X-Frontend-Id = %q; want %q", got, nicoFrontendID)
		}
		if got := r.Header.Get("X-Frontend-Version"); got != nicoFrontendVersion {
			t.Errorf("X-Frontend-Version = %q; want %q", got, nicoFrontendVersion)
		}
		if got := r.Header.Get("User-Agent"); got != "VocaBox/test" {
			t.Errorf("User-Agent = %q", got)
		}
		if got := r.Header.Get("Cookie"); !strings.Contains(got, "user_session=s") {
			t.Errorf("Cookie = %q; want the session", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"meta": map[string]any{"status": 200},
			"data": map[string]any{
				"videoId":            "sm1",
				"hasTimeInformation": true,
				"lyrics": []map[string]any{
					{"lines": []string{"1行目", "2行目"}, "startMs": 5200, "endMs": 8750},
					{"lines": []string{"次の行"}, "startMs": 9100, "endMs": 12550},
				},
			},
		})
	})

	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(server.URL)
	jar.SetCookies(u, []*http.Cookie{{Name: "user_session", Value: "s"}})
	provider := NewNicoProvider(&http.Client{Jar: jar}, server.URL, "VocaBox/test", func() bool { return true })
	doc, err := provider.Fetch(Query{VideoID: "sm1"})
	if err != nil {
		t.Fatal(err)
	}
	if !doc.Timed || len(doc.Lines) != 2 {
		t.Fatalf("doc = %#v; want two timed lines", doc)
	}
	first := doc.Lines[0]
	if first.Text != "1行目\n2行目" {
		t.Fatalf("first line text = %q; want the block's lines joined", first.Text)
	}
	if first.StartMS != 5200 || first.EndMS != 8750 {
		t.Fatalf("first line timing = %v..%v; want 5200..8750", first.StartMS, first.EndMS)
	}
	if doc.Lines[1].Text != "次の行" || doc.Lines[1].EndMS != 12550 {
		t.Fatalf("second line = %#v", doc.Lines[1])
	}
}

func TestNicoProviderReadsUntimedLyrics(t *testing.T) {
	for _, c := range []struct {
		name string
		data map[string]any
	}{
		{
			name: "no timing flag",
			data: map[string]any{
				"hasTimeInformation": false,
				"lyrics":             []map[string]any{{"lines": []string{"一行目"}, "startMs": 1000, "endMs": 2000}},
			},
		},
		{
			name: "flagged but without timings",
			data: map[string]any{
				"hasTimeInformation": true,
				"lyrics":             []map[string]any{{"lines": []string{"一行目"}}},
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"meta": map[string]any{"status": 200},
					"data": c.data,
				})
			}))
			defer server.Close()
			provider := NewNicoProvider(server.Client(), server.URL, "", func() bool { return true })
			doc, err := provider.Fetch(Query{VideoID: "sm1"})
			if err != nil {
				t.Fatal(err)
			}
			if doc.Timed {
				t.Fatalf("doc.Timed = true; want an untimed doc")
			}
			if len(doc.Lines) != 1 || doc.Lines[0].StartMS != 0 || doc.Lines[0].EndMS != 0 {
				t.Fatalf("lines = %#v; want the timings dropped", doc.Lines)
			}
		})
	}
}

func TestNicoProviderSkipsKnownNoLyrics(t *testing.T) {
	provider := NewNicoProvider(http.DefaultClient, "https://nvapi.invalid", "", func() bool { return true })
	_, err := provider.Fetch(Query{VideoID: "sm1", HasLyricsKnown: true})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Fetch error = %v; want ErrNotFound without a request", err)
	}
}

func TestNicoProviderNeedsLogin(t *testing.T) {
	provider := NewNicoProvider(nil, "https://nvapi.invalid", "", func() bool { return false })
	_, err := provider.Fetch(Query{VideoID: "sm1"})
	if !errors.Is(err, ErrLogin) {
		t.Fatalf("Fetch error = %v; want ErrLogin", err)
	}
}

func TestNicoProviderMapsFailures(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{
			name:   "unauthorized",
			status: http.StatusUnauthorized,
			body:   `{"meta":{"status":401,"errorCode":"UNAUTHORIZED"}}`,
			want:   ErrUnauthorized,
		},
		{
			name:   "forbidden",
			status: http.StatusForbidden,
			body:   `{"meta":{"status":403,"errorCode":"FORBIDDEN"}}`,
			want:   ErrForbidden,
		},
		{
			name:   "not found",
			status: http.StatusNotFound,
			body:   `{"meta":{"status":404,"errorCode":"NOT_FOUND"}}`,
			want:   ErrNotFound,
		},
		{
			name:   "access limit",
			status: http.StatusForbidden,
			body:   `{"meta":{"status":403,"errorCode":"LYRICS_ACCESS_LIMIT_EXCEEDED"}}`,
			want:   ErrLimit,
		},
		{
			name:   "server error",
			status: http.StatusInternalServerError,
			body:   `{"meta":{"status":500,"errorCode":"INTERNAL_SERVER_ERROR"}}`,
			want:   ErrFailed,
		},
		{
			name:   "unauthorized without a body",
			status: http.StatusUnauthorized,
			body:   "",
			want:   ErrUnauthorized,
		},
		{
			name:   "empty lyrics",
			status: http.StatusOK,
			body:   `{"meta":{"status":200},"data":{"hasTimeInformation":false,"lyrics":[]}}`,
			want:   ErrNotFound,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			defer server.Close()
			provider := NewNicoProvider(server.Client(), server.URL, "", func() bool { return true })
			_, err := provider.Fetch(Query{VideoID: "sm1"})
			if !errors.Is(err, c.want) {
				t.Fatalf("Fetch error = %v; want %v", err, c.want)
			}
		})
	}
}
