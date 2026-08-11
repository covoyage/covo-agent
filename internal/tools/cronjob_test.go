package tools

import (
	"testing"
	"time"
)

func TestNextCronRun(t *testing.T) {
	base := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)

	t.Run("@every 5m", func(t *testing.T) {
		next, err := nextCronRun("@every 5m", base)
		if err != nil {
			t.Fatal(err)
		}
		want := base.Add(5 * time.Minute)
		if !next.Equal(want) {
			t.Errorf("got %v, want %v", next, want)
		}
	})

	t.Run("@hourly", func(t *testing.T) {
		next, err := nextCronRun("@hourly", base)
		if err != nil {
			t.Fatal(err)
		}
		want := base.Add(1 * time.Hour)
		if !next.Equal(want) {
			t.Errorf("got %v, want %v", next, want)
		}
	})

	t.Run("@daily", func(t *testing.T) {
		next, err := nextCronRun("@daily", base)
		if err != nil {
			t.Fatal(err)
		}
		want := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		if !next.Equal(want) {
			t.Errorf("got %v, want %v", next, want)
		}
	})

	t.Run("@weekly", func(t *testing.T) {
		// 2026-05-31 is a Sunday
		next, err := nextCronRun("@weekly", base)
		if err != nil {
			t.Fatal(err)
		}
		want := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
		if !next.Equal(want) {
			t.Errorf("got %v, want %v", next, want)
		}
	})

	t.Run("@monthly", func(t *testing.T) {
		// From May 31, next month is July 1 (June 31 doesn't exist)
		next, err := nextCronRun("@monthly", base)
		if err != nil {
			t.Fatal(err)
		}
		want := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
		if !next.Equal(want) {
			t.Errorf("got %v, want %v", next, want)
		}
	})

	t.Run("invalid duration", func(t *testing.T) {
		_, err := nextCronRun("@every bad", base)
		if err == nil {
			t.Error("expected error for invalid duration")
		}
	})
}

func TestMatchesCronField(t *testing.T) {
	t.Run("wildcard", func(t *testing.T) {
		if !matchesCronField("*", 5, 0, 59) {
			t.Error("expected '*' to match any value")
		}
	})

	t.Run("exact", func(t *testing.T) {
		if !matchesCronField("5", 5, 0, 59) {
			t.Error("expected '5' to match 5")
		}
		if matchesCronField("5", 6, 0, 59) {
			t.Error("expected '5' not to match 6")
		}
	})

	t.Run("step", func(t *testing.T) {
		if !matchesCronField("*/5", 5, 0, 59) {
			t.Error("expected '*/5' to match 5")
		}
		if matchesCronField("*/5", 3, 0, 59) {
			t.Error("expected '*/5' not to match 3")
		}
	})

	t.Run("comma", func(t *testing.T) {
		if !matchesCronField("1,3,5", 3, 0, 59) {
			t.Error("expected '1,3,5' to match 3")
		}
		if matchesCronField("1,3,5", 2, 0, 59) {
			t.Error("expected '1,3,5' not to match 2")
		}
	})

	t.Run("range", func(t *testing.T) {
		if !matchesCronField("3-7", 5, 0, 59) {
			t.Error("expected '3-7' to match 5")
		}
		if matchesCronField("3-7", 8, 0, 59) {
			t.Error("expected '3-7' not to match 8")
		}
	})
}
