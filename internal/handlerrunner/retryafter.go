package handlerrunner

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RetryAfterError carries a vendor-stated cooldown derived from an HTTP
// Retry-After response header. Handlers wrap their error in this type when
// the destination signals a specific retry interval; the runner's retry
// strategy then sleeps at least that long before the next attempt.
//
// Per RFC 9110 §10.2.3, Retry-After may be either a non-negative integer of
// seconds or an HTTP-date. Both forms are parsed.
type RetryAfterError struct {
	Cooldown time.Duration
	Cause    error
}

// Error implements error.
func (e *RetryAfterError) Error() string {
	if e.Cause != nil {
		return "retry-after " + e.Cooldown.String() + ": " + e.Cause.Error()
	}
	return "retry-after " + e.Cooldown.String()
}

// Unwrap exposes the underlying cause for errors.Is / errors.As walks.
func (e *RetryAfterError) Unwrap() error { return e.Cause }

// ParseRetryAfter reads an HTTP Retry-After header value. Returns the
// requested cooldown and ok=true when the value is a valid delta-seconds
// or HTTP-date in the future. now is injected for testability.
func ParseRetryAfter(header string, now time.Time) (time.Duration, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(header); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(header); err == nil {
		d := t.Sub(now)
		if d < 0 {
			return 0, false
		}
		return d, true
	}
	return 0, false
}

// WrapHTTPError returns err unchanged when resp is nil or when resp does not
// carry a Retry-After header. Otherwise returns a *RetryAfterError that the
// retry strategy honours via PreservedDelay (when supported by the runner).
func WrapHTTPError(err error, resp *http.Response) error {
	if err == nil || resp == nil {
		return err
	}
	d, ok := ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
	if !ok {
		return err
	}
	return &RetryAfterError{Cooldown: d, Cause: err}
}
