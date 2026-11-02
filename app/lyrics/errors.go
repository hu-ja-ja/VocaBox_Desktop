package lyrics

import "errors"

// The failures a source reports. The UI maps them to the app's messages;
// the package itself stays free of the UI's language.
var (
	// ErrNotFound says the track has no lyrics in this source, whether
	// because none were registered or because the source does not know it.
	ErrNotFound = errors.New("lyrics: not found")
	// ErrLogin says the source needs a signed-in session.
	ErrLogin = errors.New("lyrics: login required")
	// ErrLimit says the account's monthly lyrics allowance is spent.
	ErrLimit = errors.New("lyrics: access limit exceeded")
	// ErrForbidden says the source refuses this track, as niconico does for
	// channel videos.
	ErrForbidden = errors.New("lyrics: forbidden")
	// ErrUnauthorized says the stored session is no longer accepted.
	ErrUnauthorized = errors.New("lyrics: unauthorized")
	// ErrFailed is any other failure of a source.
	ErrFailed = errors.New("lyrics: fetch failed")
)
