package ossandbox

import (
	"os"
	"sort"
	"testing"
)

func TestScrubNoisyEnv_RemovesMallocVars(t *testing.T) {
	// Set a few noisy vars.
	t.Setenv("MallocStackLogging", "1")
	t.Setenv("MallocStackLoggingNoHistory", "1")
	t.Setenv("MallocNanoZone", "1")
	t.Setenv("MallocLogFile", "/tmp/malloc.log")
	// Set a non-noisy var that must survive.
	t.Setenv("PATH", "/usr/bin:/bin")
	// Ensure the opt-out is not set.
	t.Setenv("COVO_KEEP_MALLOC_DEBUG", "")

	ScrubNoisyEnv()

	for _, name := range []string{"MallocStackLogging", "MallocStackLoggingNoHistory", "MallocNanoZone", "MallocLogFile"} {
		if _, ok := os.LookupEnv(name); ok {
			t.Errorf("%q should have been unset by ScrubNoisyEnv", name)
		}
	}
	if v := os.Getenv("PATH"); v != "/usr/bin:/bin" {
		t.Errorf("PATH should survive scrubbing, got %q", v)
	}
}

func TestScrubNoisyEnv_OptOutPreservesVars(t *testing.T) {
	t.Setenv("MallocStackLogging", "1")
	t.Setenv("COVO_KEEP_MALLOC_DEBUG", "1")

	ScrubNoisyEnv()

	if _, ok := os.LookupEnv("MallocStackLogging"); !ok {
		t.Error("MallocStackLogging should be preserved when COVO_KEEP_MALLOC_DEBUG=1")
	}
}

func TestScrubNoisyEnv_OptOutTruePreservesVars(t *testing.T) {
	t.Setenv("MallocStackLogging", "1")
	t.Setenv("COVO_KEEP_MALLOC_DEBUG", "true")

	ScrubNoisyEnv()

	if _, ok := os.LookupEnv("MallocStackLogging"); !ok {
		t.Error("MallocStackLogging should be preserved when COVO_KEEP_MALLOC_DEBUG=true")
	}
}

func TestScrubEnvSlice_RemovesNoisyVars(t *testing.T) {
	t.Setenv("COVO_KEEP_MALLOC_DEBUG", "")

	env := []string{
		"PATH=/usr/bin",
		"HOME=/root",
		"MallocStackLogging=1",
		"MallocStackLoggingNoHistory=1",
		"MallocNanoZone=1",
		"MY_VAR=hello",
	}

	result := ScrubEnvSlice(env)

	sort.Strings(result)
	have := make(map[string]string)
	for _, kv := range result {
		// just collect names
		name := kv
		if idx := indexByte(kv, '='); idx >= 0 {
			name = kv[:idx]
			have[name] = kv[idx+1:]
		} else {
			have[name] = ""
		}
	}

	if have["PATH"] != "/usr/bin" {
		t.Errorf("PATH should survive, got %q", have["PATH"])
	}
	if have["HOME"] != "/root" {
		t.Errorf("HOME should survive, got %q", have["HOME"])
	}
	if have["MY_VAR"] != "hello" {
		t.Errorf("MY_VAR should survive, got %q", have["MY_VAR"])
	}
	for _, noisy := range []string{"MallocStackLogging", "MallocStackLoggingNoHistory", "MallocNanoZone"} {
		if _, ok := have[noisy]; ok {
			t.Errorf("%q should have been stripped from slice", noisy)
		}
	}
}

func TestScrubEnvSlice_OptOutPreserves(t *testing.T) {
	t.Setenv("COVO_KEEP_MALLOC_DEBUG", "1")

	env := []string{
		"MallocStackLogging=1",
		"PATH=/usr/bin",
	}

	result := ScrubEnvSlice(env)

	found := false
	for _, kv := range result {
		if kv == "MallocStackLogging=1" {
			found = true
		}
	}
	if !found {
		t.Error("MallocStackLogging should be preserved in slice when COVO_KEEP_MALLOC_DEBUG=1")
	}
}

func TestScrubEnvSlice_CaseInsensitive(t *testing.T) {
	t.Setenv("COVO_KEEP_MALLOC_DEBUG", "")

	env := []string{
		"mallocstacklogging=1", // lowercase variant
		"PATH=/usr/bin",
	}

	result := ScrubEnvSlice(env)

	for _, kv := range result {
		name := kv
		if idx := indexByte(kv, '='); idx >= 0 {
			name = kv[:idx]
		}
		if name == "mallocstacklogging" {
			t.Error("lowercase mallocstacklogging should also be stripped (case-insensitive)")
		}
	}
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
