package main

import (
	"slices"
	"testing"
	"time"
)

func TestParseClockAndNextRun(t *testing.T) {
	at, err := parseClock("03:30")
	if err != nil || at != 3*time.Hour+30*time.Minute {
		t.Fatalf("parse = %v, %v", at, err)
	}
	for _, bad := range []string{"", "3", "24:00", "03:60", "ab:cd", "03-00"} {
		if _, err := parseClock(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	loc := time.UTC
	before := time.Date(2026, 11, 22, 1, 0, 0, 0, loc)
	if got := nextRun(before, at); !got.Equal(time.Date(2026, 11, 22, 3, 30, 0, 0, loc)) {
		t.Errorf("before the hour: %v", got)
	}
	after := time.Date(2026, 11, 22, 3, 30, 0, 0, loc)
	if got := nextRun(after, at); !got.Equal(time.Date(2026, 11, 23, 3, 30, 0, 0, loc)) {
		t.Errorf("at the hour exactly goes to tomorrow: %v", got)
	}
	if got := nextRun(time.Date(2026, 12, 31, 23, 0, 0, 0, loc), at); !got.Equal(time.Date(2027, 1, 1, 3, 30, 0, 0, loc)) {
		t.Errorf("across the year: %v", got)
	}
}

func TestPgEnvKeepsThePasswordOutOfTheCommandLine(t *testing.T) {
	env, err := pgEnv("postgres://cobogo_backup:s3nh4@postgres:5432/cobogo_core?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"PGHOST=postgres", "PGPORT=5432", "PGUSER=cobogo_backup", "PGPASSWORD=s3nh4", "PGDATABASE=cobogo_core", "PGSSLMODE=disable"} {
		if !slices.Contains(env, want) {
			t.Errorf("missing %s in %v", want, env)
		}
	}
	if _, err := pgEnv("não é uma url ::"); err == nil {
		t.Error("an invalid DSN must be refused")
	}
}
