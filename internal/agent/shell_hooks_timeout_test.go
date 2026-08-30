package agent

import (
	"testing"
	"time"
)

func TestHookExecutionTimeout(t *testing.T) {
	cases := []struct {
		name string
		spec *ShellHookSpec
		want time.Duration
	}{
		{
			name: "configured timeout is honored, not truncated to 5s",
			spec: &ShellHookSpec{Timeout: 10 * time.Second},
			want: 10 * time.Second,
		},
		{
			name: "zero timeout falls back to default",
			spec: &ShellHookSpec{},
			want: defaultHookTimeout,
		},
		{
			name: "negative timeout falls back to default",
			spec: &ShellHookSpec{Timeout: -1},
			want: defaultHookTimeout,
		},
		{
			name: "timeout above the cap is clamped",
			spec: &ShellHookSpec{Timeout: maxHookTimeout + time.Hour},
			want: maxHookTimeout,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hookExecutionTimeout(tc.spec); got != tc.want {
				t.Errorf("hookExecutionTimeout(%v) = %v, want %v", tc.spec.Timeout, got, tc.want)
			}
		})
	}
}
