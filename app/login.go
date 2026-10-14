package main

import (
	"errors"
	"log"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// The sign-in flow opens niconico's own login page in a web view: the
// page's anti-bot challenge, password and two-factor steps all happen
// there, and the app only takes the session cookies the page ends up with,
// out of the cookie store the web views share. The password itself is
// never seen by the app.
const (
	// loginURL is niconico's login page, which redirects to its current
	// sign-in screen.
	loginURL = "https://account.nicovideo.jp/login"
	// loginPollInterval is how often the flow asks the cookie store for the
	// session while the window is open.
	loginPollInterval = 500 * time.Millisecond
)

// sessionCookieHeader picks the session cookies out of the store's cookies
// for nicovideo: the Cookie header to store, "" while the page has none.
func sessionCookieHeader(cookies []mygo.Cookie) string {
	var parts []string
	seen := map[string]bool{}
	for _, c := range cookies {
		if seen[c.Name] || c.Value == "" {
			continue
		}
		if !slices.Contains(sessionCookieNames, c.Name) {
			continue
		}
		seen[c.Name] = true
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

// openLogin opens the sign-in window and watches it for a session; a
// window already open is brought to the front instead.
func (app *nativeApp) openLogin() {
	if window := app.loginWindow; window != nil && !window.IsDestroyed() {
		window.Focus()
		return
	}
	window := mygo.NewWindow(mygo.WindowOptions{
		Title:         "ニコニコにログイン",
		URL:           loginURL,
		Width:         480,
		Height:        780,
		DisableResize: true,
	})
	if window.IsDestroyed() {
		app.setError(errors.New("ログイン画面を開けませんでした"))
		return
	}
	app.loginWindow = window
	go app.watchLogin(window)
}

// watchLogin polls the cookie store for the session cookies nicovideo sets
// once the login went through, and completes the sign-in with them. It
// ends when the window closes or the session is taken.
func (app *nativeApp) watchLogin(window *mygo.Window) {
	// The store is only read once the login page's DOM is ready: asking
	// before a window shows a page makes mygo's Windows backend build a
	// hidden web view for the call, which races the login window's own web
	// view being made and crashes the message loop (api-review; see 要件/).
	// The page's DOM ready means its web view is up.
	var pageReady atomic.Bool
	if page := window.Page(); page != nil {
		page.OnDOMReady(func() { pageReady.Store(true) })
	}
	loggedOnce := false
	for !window.IsDestroyed() {
		if !pageReady.Load() {
			time.Sleep(loginPollInterval)
			continue
		}
		// The store holds every cookie of the app's web views; the filter
		// keeps nicovideo's, the session among them once signed in.
		cookies, err := mygo.Cookies.List(mygo.CookieFilter{Domain: "nicovideo.jp"})
		if err != nil {
			// A lasting failure is worth one word on the log.
			if !loggedOnce {
				loggedOnce = true
				log.Printf("login: reading the cookie store: %v", err)
			}
		} else if header := sessionCookieHeader(cookies); header != "" {
			app.completeLogin(window, header)
			return
		}
		time.Sleep(loginPollInterval)
	}
	app.update(func() { app.loginWindow = nil })
}

// completeLogin verifies the session the login page ended up with, stores
// it, and closes the window. The browser session the window kept is
// cleared, so the cookies live on only in the app's DPAPI file.
func (app *nativeApp) completeLogin(window *mygo.Window, header string) {
	verifyErr := error(nil)
	if app.playback != nil {
		if err := app.playback.nico.SetSession(header); err != nil {
			verifyErr = err
		} else {
			verifyErr = app.playback.nico.VerifySession()
		}
	}
	if errors.Is(verifyErr, errSessionExpired) {
		// niconico refused the session at once: nothing was signed in.
		app.clearSession()
		app.finishLogin(window, "ログイン情報を確認できませんでした。もう一度お試しください。", true)
		return
	}
	if verifyErr != nil {
		// Could not tell (a network failure, say): keep the session and
		// let the first playback show whether it holds.
		log.Printf("login: verifying the session: %v", verifyErr)
	}
	if err := app.session.Save(header); err != nil {
		app.finishLogin(window, err.Error(), true)
		return
	}
	// The cookies are the app's now: forget the browser session the sign-in
	// window kept.
	if err := mygo.App.ClearBrowsingData(); err != nil {
		log.Printf("login: clearing the web view data: %v", err)
	}
	app.finishLogin(window, "ログインしました。", false)
	app.loadAccount()
}

// loadAccount fetches the signed-in account's name and picture in the
// background; the sidebar shows them once they arrive. A failure leaves
// the row as it is, so it is only worth a line on the log.
func (app *nativeApp) loadAccount() {
	if app.playback == nil {
		return
	}
	go func() {
		user, icon, err := app.fetchAccount()
		if err != nil {
			log.Printf("account: %v", err)
			return
		}
		app.update(func() {
			if !app.loggedIn {
				return
			}
			app.accountName = user.Nickname
			if icon != nil {
				// Decoded here, where the thumbnails are.
				if bitmap, err := ui.DecodeBitmap(icon); err == nil && thumbSizeOK(bitmap) {
					app.accountIcon = bitmap
				}
			}
		})
	}()
}

// fetchAccount asks niconico for the signed-in account and the bytes of
// its picture; the caller decodes the picture where bitmaps may be made.
func (app *nativeApp) fetchAccount() (nicoUser, []byte, error) {
	user, err := app.playback.nico.User()
	if err != nil {
		return nicoUser{}, nil, err
	}
	if user.IconURL == "" {
		return user, nil, nil
	}
	// The picture rules of the thumbnails apply: the allowed host, an
	// image response, the size bound, and the shared limiter.
	body, err := app.mylist.fetchThumbnail(user.IconURL)
	if err != nil {
		log.Printf("account: %v", err)
		return user, nil, nil
	}
	return user, body, nil
}

// finishLogin closes the sign-in window and reports how it went, on the
// UI thread.
func (app *nativeApp) finishLogin(window *mygo.Window, status string, failed bool) {
	app.update(func() {
		app.loginWindow = nil
		window.Close()
		if failed {
			app.loggedIn = false
			app.setError(errors.New(status))
			return
		}
		app.loggedIn = true
		app.setStatus(status)
	})
}

// logout forgets the session the user signed in with.
func (app *nativeApp) logout() {
	app.clearSession()
	app.setStatus("ログアウトしました。")
}

// sessionExpired forgets a session niconico no longer accepts, so the next
// track plays as a guest; the player itself reports the failure.
func (app *nativeApp) sessionExpired() {
	app.clearSession()
}

// clearSession forgets the stored session and returns the player to guest
// playback, and forgets the account the sidebar showed.
func (app *nativeApp) clearSession() {
	if err := app.session.Clear(); err != nil {
		log.Printf("session: %v", err)
	}
	if app.playback != nil {
		app.playback.nico.ClearSession()
	}
	app.loggedIn = false
	app.accountName, app.accountIcon = "", nil
}

// update runs fn on the UI thread; without a window, as in tests, it runs
// inline.
func (app *nativeApp) update(fn func()) {
	if app.window == nil {
		fn()
		return
	}
	app.window.Update(fn)
}
