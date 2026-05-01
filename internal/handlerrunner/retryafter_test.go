package handlerrunner_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/felixgeelhaar/praxis/internal/handlerrunner"
)

func TestParseRetryAfter_Seconds(t *testing.T) {
	d, ok := handlerrunner.ParseRetryAfter("42", time.Now())
	if !ok || d != 42*time.Second {
		t.Errorf("got %s, %v want 42s, true", d, ok)
	}
}

func TestParseRetryAfter_HTTPDate(t *testing.T) {
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	future := now.Add(2 * time.Minute).Format(http.TimeFormat)
	d, ok := handlerrunner.ParseRetryAfter(future, now)
	if !ok {
		t.Fatalf("not parsed")
	}
	if d < 119*time.Second || d > 2*time.Minute {
		t.Errorf("d=%s want ~2m", d)
	}
}

func TestParseRetryAfter_PastDateRejected(t *testing.T) {
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	past := now.Add(-1 * time.Hour).Format(http.TimeFormat)
	if _, ok := handlerrunner.ParseRetryAfter(past, now); ok {
		t.Errorf("past date should be rejected")
	}
}

func TestParseRetryAfter_Garbage(t *testing.T) {
	if _, ok := handlerrunner.ParseRetryAfter("abc", time.Now()); ok {
		t.Errorf("garbage should be rejected")
	}
	if _, ok := handlerrunner.ParseRetryAfter("", time.Now()); ok {
		t.Errorf("empty should be rejected")
	}
	if _, ok := handlerrunner.ParseRetryAfter("-1", time.Now()); ok {
		t.Errorf("negative should be rejected")
	}
}

func TestRetryAfterError_UnwrapAndIsRetryable(t *testing.T) {
	cause := errors.New("vendor 429 too many")
	err := &handlerrunner.RetryAfterError{Cooldown: 5 * time.Second, Cause: cause}
	if !errors.Is(err, cause) {
		t.Errorf("errors.Is should reach the cause")
	}
	if !handlerrunner.IsRetryable(err) {
		t.Errorf("Retry-After errors should be retryable")
	}
}

func TestWrapHTTPError(t *testing.T) {
	cause := errors.New("rate limited")
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", "10")

	wrapped := handlerrunner.WrapHTTPError(cause, resp)
	var ra *handlerrunner.RetryAfterError
	if !errors.As(wrapped, &ra) {
		t.Fatalf("wrapped not a RetryAfterError: %T", wrapped)
	}
	if ra.Cooldown != 10*time.Second {
		t.Errorf("Cooldown=%s want 10s", ra.Cooldown)
	}
}

func TestWrapHTTPError_NoHeader(t *testing.T) {
	cause := errors.New("rate limited")
	resp := &http.Response{Header: http.Header{}}
	if got := handlerrunner.WrapHTTPError(cause, resp); got != cause {
		t.Errorf("expected cause unchanged when no Retry-After header")
	}
}
