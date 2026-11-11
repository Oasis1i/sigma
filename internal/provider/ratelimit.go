package provider

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RateLimitedError is a 429 from an upstream. Reset is the best time we
// could read from the response headers. Zero means the server did not say.
type RateLimitedError struct {
	Provider string
	Reset    time.Time
	Detail   string
}

func (e *RateLimitedError) Error() string {
	if e == nil {
		return "rate limited"
	}
	name := e.Provider
	if name == "" {
		name = "provider"
	}
	detail := strings.TrimSpace(e.Detail)
	if !e.Reset.IsZero() {
		when := e.Reset.Local().Format(time.RFC3339)
		if detail == "" {
			return fmt.Sprintf("%s rate limited; resets at %s", name, when)
		}
		return fmt.Sprintf("%s rate limited; resets at %s (%s)", name, when, detail)
	}
	if detail == "" {
		return fmt.Sprintf("%s rate limited", name)
	}
	return fmt.Sprintf("%s rate limited (%s)", name, detail)
}

// rateLimitError reads a 429 response and closes nothing. The caller closes
// the body. The body is consumed here.
func rateLimitError(provider string, resp *http.Response) error {
	var detail string
	if resp.Body != nil {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		detail = strings.TrimSpace(string(b))
		detail = oneLine(detail)
	}
	reset := resetTime(resp.Header, time.Now())
	return &RateLimitedError{Provider: provider, Reset: reset, Detail: detail}
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 180 {
		return s[:180]
	}
	return s
}

// resetTime looks at the headers vendors use for a 429. now anchors relative
// delays such as Retry-After seconds.
func resetTime(h http.Header, now time.Time) time.Time {
	if h == nil {
		return time.Time{}
	}
	if v := h.Get("Retry-After"); v != "" {
		if sec, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && sec >= 0 {
			return now.Add(time.Duration(sec) * time.Second)
		}
		if t, err := http.ParseTime(v); err == nil {
			return t
		}
	}
	for _, key := range []string{
		"x-codex-primary-reset-at",
		"anthropic-ratelimit-requests-reset",
		"anthropic-ratelimit-tokens-reset",
	} {
		if t, ok := parseResetValue(h.Get(key), now); ok {
			return t
		}
	}
	if t, ok := parseOpenAIDelay(h.Get("x-ratelimit-reset-requests"), now); ok {
		return t
	}
	return time.Time{}
}

func parseResetValue(v string, now time.Time) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, true
	}
	if sec, err := strconv.ParseInt(v, 10, 64); err == nil {
		// Unix seconds when the number is large, otherwise a delay.
		if sec > 1_000_000_000 {
			return time.Unix(sec, 0), true
		}
		if sec >= 0 {
			return now.Add(time.Duration(sec) * time.Second), true
		}
	}
	return time.Time{}, false
}

func parseOpenAIDelay(v string, now time.Time) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, false
	}
	var n int
	var unit string
	if _, err := fmt.Sscanf(v, "%d%s", &n, &unit); err != nil || n < 0 {
		return time.Time{}, false
	}
	switch unit {
	case "s", "sec", "secs":
		return now.Add(time.Duration(n) * time.Second), true
	case "m", "min", "mins":
		return now.Add(time.Duration(n) * time.Minute), true
	case "h", "hr", "hrs":
		return now.Add(time.Duration(n) * time.Hour), true
	default:
		return time.Time{}, false
	}
}
