package cactus

import "time"

// DateFact returns the date system fact for t, such as
// "date: 2026-07-21 Tue 14:30". The engine resolves relative dates, such as
// "tomorrow" or "March 4th", only against this fact; without it such requests
// return no call. Facts are fixed when the agent is created.
func DateFact(t time.Time) string {
	return "date: " + t.Format("2006-01-02 Mon 15:04")
}
