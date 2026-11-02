package lyrics

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// maxSourceBody bounds one response of a fallback source. Its search and
// lyrics answers are far smaller than a track's list.
const maxSourceBody = 2 << 20

// sourceBody reads a source's response, bounded, and reports a status the
// source did not answer 2xx for as ErrFailed.
func sourceBody(resp *http.Response, limit int64) ([]byte, error) {
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%w: status %d", ErrFailed, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil, ErrFailed
	}
	return body, nil
}

// sourceJSON sends req and decodes its JSON answer. The caller checks the
// source's own status code.
func sourceJSON(client *http.Client, req *http.Request, v any) error {
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrFailed, err)
	}
	body, err := sourceBody(resp, maxSourceBody)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("%w: %v", ErrFailed, err)
	}
	return nil
}
