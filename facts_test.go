package cactus

import (
	"testing"
	"time"
)

func TestDateFact(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.July, 21, 14, 30, 59, 0, time.UTC)
	if got, want := DateFact(at), "date: 2026-07-21 Tue 14:30"; got != want {
		t.Fatalf("DateFact() = %q, want %q", got, want)
	}
	if got := dateFactPattern.ReplaceAllString(DateFact(at), ""); got != "" {
		t.Fatalf("date fact pattern leaves %q", got)
	}
}
