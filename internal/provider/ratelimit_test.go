package provider

import (
	"net/http"
	"testing"
	"time"
)

func TestResetTimeHeaders(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	h := http.Header{}
	h.Set("Retry-After", "30")
	got := resetTime(h, now)
	if !got.Equal(now.Add(30 * time.Second)) {
		t.Fatalf("retry-after %s", got)
	}
	h = http.Header{}
	h.Set("x-codex-primary-reset-at", "1800000000")
	got = resetTime(h, now)
	if !got.Equal(time.Unix(1800000000, 0)) {
		t.Fatalf("unix %s", got)
	}
	h = http.Header{}
	h.Set("anthropic-ratelimit-requests-reset", "2026-11-11T12:05:00Z")
	got = resetTime(h, now)
	if got.Format(time.RFC3339) != "2026-11-11T12:05:00Z" {
		t.Fatalf("rfc3339 %s", got)
	}
	err := (&RateLimitedError{Provider: "chatgpt", Reset: got}).Error()
	if !contains(err, "chatgpt rate limited") || !contains(err, "2026-11-11T12:05:00Z") {
		t.Fatal(err)
	}
}

func contains(s, part string) bool {
	return len(part) == 0 || (len(s) >= len(part) && (s == part || len(s) > 0 && index(s, part) >= 0))
}

func index(s, part string) int {
	for i := 0; i+len(part) <= len(s); i++ {
		if s[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}
