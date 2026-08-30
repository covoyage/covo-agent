package automation

import "testing"

func TestShortID(t *testing.T) {
	cases := []struct{ id, want string }{
		{"job_1234567890abcdef", "job_1234"},
		{"short", "short"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := shortID(tc.id); got != tc.want {
			t.Errorf("shortID(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

func TestCronCommandHasRunDue(t *testing.T) {
	for _, sub := range NewCronCommand().Commands() {
		if sub.Name() == "run-due" {
			return
		}
	}
	t.Error("cron command is missing the run-due subcommand")
}
