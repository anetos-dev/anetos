// SPDX-License-Identifier: Apache-2.0

package queue

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestNewID(t *testing.T) {
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	prev := ""
	seen := map[string]bool{}
	for i := range 10000 {
		id := newID()
		if !uuid.MatchString(id) {
			t.Fatalf("newID = %q, not a UUIDv7", id)
		}
		if id <= prev || seen[id] {
			t.Fatalf("ID %d: %q doesn't sort after %q", i, id, prev)
		}
		seen[id] = true
		prev = id
	}
	ms := time.Now().UnixMilli()
	id := newID()
	var got int64
	for _, c := range id[:8] + id[9:13] {
		got = got<<4 | int64(hexVal(c))
	}
	if got < ms || got > ms+1000 {
		t.Errorf("newID's time = %d, want about %d", got, ms)
	}
}

func hexVal(c rune) int {
	if c >= 'a' {
		return int(c-'a') + 10
	}
	return int(c - '0')
}

func TestBackoff(t *testing.T) {
	q := NewWithStore(nil, Config{Backoff: time.Second, MaxBackoff: 5 * time.Second})
	near := func(got, want time.Duration) bool { return got >= want*8/10 && got <= want*12/10 }
	for attempt, want := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 4: 5 * time.Second, 60: 5 * time.Second} {
		if got := q.backoff(nil, attempt); !near(got, want) {
			t.Errorf("backoff(%d) = %s, want about %s", attempt, got, want)
		}
	}
	jt := &jobType{backoff: []time.Duration{time.Second, 3 * time.Second}}
	for attempt, want := range map[int]time.Duration{1: time.Second, 2: 3 * time.Second, 9: 3 * time.Second} {
		if got := q.backoff(jt, attempt); !near(got, want) {
			t.Errorf("Backoff list: backoff(%d) = %s, want about %s", attempt, got, want)
		}
	}
	if d := NewWithStore(nil, Config{}).Config(); d.Tries != 3 || d.Timeout != time.Minute || d.Backoff != 10*time.Second ||
		d.MaxBackoff != 10*time.Minute || d.Default != "default" || d.Table != "jobs" || d.FailedTable != "failed_jobs" || d.PollInterval != time.Second {
		t.Errorf("defaults = %+v", d)
	}
}

func TestErrorText(t *testing.T) {
	long := strings.Repeat("é", maxErrorText) // 2 bytes each
	for in, want := range map[string]string{
		"plain":            "plain",
		"nul\x00 and \xff": "nul\uFFFD and \uFFFD",
	} {
		if got := errorText(errors.New(in)); got != want {
			t.Errorf("errorText(%q) = %q, want %q", in, got, want)
		}
	}
	got := errorText(errors.New(long))
	if len(got) > maxErrorText+len("…") || !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
		t.Errorf("errorText of a long error: %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
}

func TestLease(t *testing.T) {
	q := NewWithStore(nil, Config{Timeout: time.Minute})
	if got := q.lease(); got != time.Minute+leaseMargin {
		t.Errorf("lease = %s", got)
	}
	type long struct{ Job }
	if err := Register[long](q, Timeout(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := q.lease(); got != time.Hour+leaseMargin {
		t.Errorf("lease with a longer timeout = %s", got)
	}
}
