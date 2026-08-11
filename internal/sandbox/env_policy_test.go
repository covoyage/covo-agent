package sandbox

import (
	"os"
	"sort"
	"testing"
)

func TestEnvPolicy_InheritAll_StripsSecrets(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"HOME=/root",
		"API_KEY=secret123",
		"GITHUB_TOKEN=ghp_xxx",
		"DATABASE_URL=postgres://user:pass@host",
		"MY_VAR=hello",
	}
	policy := EnvPolicy{Mode: EnvInheritAll}
	result := policy.FilterEnv(env)

	sort.Strings(result)
	for _, e := range result {
		name := e
		if idx := indexByte(e, '='); idx >= 0 {
			name = e[:idx]
		}
		if isSecretEnv(name) {
			t.Errorf("secret var %q should have been stripped", name)
		}
	}
}

func TestEnvPolicy_InheritCore(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"HOME=/root",
		"API_KEY=secret123",
		"MY_CUSTOM_VAR=value",
		"TERM=xterm",
	}
	policy := EnvPolicy{Mode: EnvInheritCore}
	result := policy.FilterEnv(env)

	have := make(map[string]bool)
	for _, e := range result {
		name := e
		if idx := indexByte(e, '='); idx >= 0 {
			name = e[:idx]
		}
		have[name] = true
	}

	if !have["PATH"] {
		t.Error("PATH should be present in core mode")
	}
	if !have["HOME"] {
		t.Error("HOME should be present in core mode")
	}
	if !have["TERM"] {
		t.Error("TERM should be present in core mode")
	}
	if have["MY_CUSTOM_VAR"] {
		t.Error("MY_CUSTOM_VAR should NOT be present in core mode")
	}
	if have["API_KEY"] {
		t.Error("API_KEY should NOT be present (secret)")
	}
}

func TestEnvPolicy_InheritNone(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"HOME=/root",
		"API_KEY=secret123",
	}
	policy := EnvPolicy{Mode: EnvInheritNone}
	result := policy.FilterEnv(env)

	if len(result) != 0 {
		t.Errorf("expected 0 vars in none mode, got %d: %v", len(result), result)
	}
}

func TestEnvPolicy_IncludeOnly(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"HOME=/root",
		"API_KEY=secret123",
		"CUSTOM=value",
	}
	policy := EnvPolicy{
		IncludeOnly: []string{"PATH", "CUSTOM"},
	}
	result := policy.FilterEnv(env)

	have := make(map[string]bool)
	for _, e := range result {
		name := e
		if idx := indexByte(e, '='); idx >= 0 {
			name = e[:idx]
		}
		have[name] = true
	}

	if !have["PATH"] {
		t.Error("PATH should be present (include_only)")
	}
	if !have["CUSTOM"] {
		t.Error("CUSTOM should be present (include_only)")
	}
	if have["HOME"] {
		t.Error("HOME should NOT be present (not in include_only)")
	}
	if have["API_KEY"] {
		t.Error("API_KEY should NOT be present (not in include_only)")
	}
}

func TestEnvPolicy_Exclude(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"AWS_REGION=us-east-1",
		"AWS_SECRET_ACCESS_KEY=xxx",
		"MY_VAR=hello",
	}
	policy := EnvPolicy{
		Mode:    EnvInheritAll,
		Exclude: []string{"AWS_*"},
	}
	result := policy.FilterEnv(env)

	have := make(map[string]bool)
	for _, e := range result {
		name := e
		if idx := indexByte(e, '='); idx >= 0 {
			name = e[:idx]
		}
		have[name] = true
	}

	if !have["PATH"] {
		t.Error("PATH should be present")
	}
	if !have["MY_VAR"] {
		t.Error("MY_VAR should be present")
	}
	if have["AWS_REGION"] {
		t.Error("AWS_REGION should be excluded by AWS_* pattern")
	}
	if have["AWS_SECRET_ACCESS_KEY"] {
		t.Error("AWS_SECRET_ACCESS_KEY should be excluded (both by pattern and secret)")
	}
}

func TestEnvPolicy_Set(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
	}
	policy := EnvPolicy{
		Mode: EnvInheritNone,
		Set:  map[string]string{"MY_INJECTED": "value123"},
	}
	result := policy.FilterEnv(env)

	if len(result) != 1 {
		t.Fatalf("expected 1 var, got %d: %v", len(result), result)
	}
	if result[0] != "MY_INJECTED=value123" {
		t.Errorf("expected MY_INJECTED=value123, got %s", result[0])
	}
}

func TestEnvPolicy_SetOverridesInherited(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"MY_VAR=original",
	}
	policy := EnvPolicy{
		Mode: EnvInheritAll,
		Set:  map[string]string{"MY_VAR": "overridden"},
	}
	result := policy.FilterEnv(env)

	for _, e := range result {
		if e == "MY_VAR=overridden" {
			return // found
		}
	}
	t.Error("expected MY_VAR=overridden in result")
}

func TestEnvPolicy_PWDNotStripped(t *testing.T) {
	env := []string{
		"PWD=/workspace",
		"OLDPWD=/home",
		"PASSWORD=secret",
	}
	policy := EnvPolicy{Mode: EnvInheritAll}
	result := policy.FilterEnv(env)

	have := make(map[string]bool)
	for _, e := range result {
		name := e
		if idx := indexByte(e, '='); idx >= 0 {
			name = e[:idx]
		}
		have[name] = true
	}

	if !have["PWD"] {
		t.Error("PWD should NOT be stripped (it's not a password)")
	}
	if !have["OLDPWD"] {
		t.Error("OLDPWD should NOT be stripped")
	}
	if have["PASSWORD"] {
		t.Error("PASSWORD should be stripped")
	}
}

func TestIsSecretEnv(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"API_KEY", true},
		{"GITHUB_TOKEN", true},
		{"DATABASE_URL", true},
		{"AWS_SECRET_ACCESS_KEY", true},
		{"MY_SECRET", true},
		{"PRIVATE_KEY", true},
		{"AUTHORIZATION", true},
		{"PASSWORD", true},
		{"PWD", false},       // not a password
		{"PATH", false},
		{"HOME", false},
		{"MY_VAR", false},
		{"EDITOR", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSecretEnv(tt.name); got != tt.want {
				t.Errorf("isSecretEnv(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestEnvPolicyFromEnv(t *testing.T) {
	t.Setenv("COVO_ENV_INHERIT", "core")
	t.Setenv("COVO_ENV_EXCLUDE", "AWS_*,GITHUB_*")

	policy := EnvPolicyFromEnv()

	if policy.Mode != EnvInheritCore {
		t.Errorf("expected mode core, got %s", policy.Mode)
	}
	if len(policy.Exclude) != 2 {
		t.Errorf("expected 2 exclude patterns, got %d", len(policy.Exclude))
	}
}

func TestEnvPolicy_StripsNoisyMallocVars(t *testing.T) {
	t.Setenv("COVO_KEEP_MALLOC_DEBUG", "")

	env := []string{
		"PATH=/usr/bin",
		"MallocStackLogging=1",
		"MallocStackLoggingNoHistory=1",
		"MallocNanoZone=1",
	}
	policy := EnvPolicy{Mode: EnvInheritAll}
	result := policy.FilterEnv(env)

	have := make(map[string]bool)
	for _, e := range result {
		name := e
		if idx := indexByte(e, '='); idx >= 0 {
			name = e[:idx]
		}
		have[name] = true
	}

	if !have["PATH"] {
		t.Error("PATH should survive")
	}
	for _, noisy := range []string{"MallocStackLogging", "MallocStackLoggingNoHistory", "MallocNanoZone"} {
		if have[noisy] {
			t.Errorf("%q should be stripped as noisy malloc var", noisy)
		}
	}
}

func TestEnvPolicy_StripsNoisyMallocVarsEvenWhenInjectedViaSet(t *testing.T) {
	t.Setenv("COVO_KEEP_MALLOC_DEBUG", "")

	policy := EnvPolicy{
		Mode: EnvInheritNone,
		Set:  map[string]string{"MallocStackLogging": "1"},
	}
	result := policy.FilterEnv([]string{})

	for _, e := range result {
		name := e
		if idx := indexByte(e, '='); idx >= 0 {
			name = e[:idx]
		}
		if name == "MallocStackLogging" {
			t.Error("MallocStackLogging should be stripped even when injected via Set")
		}
	}
}

func TestEnvPolicy_KeepsNoisyMallocVarsWhenOptIn(t *testing.T) {
	t.Setenv("COVO_KEEP_MALLOC_DEBUG", "1")

	env := []string{
		"MallocStackLogging=1",
	}
	policy := EnvPolicy{Mode: EnvInheritAll}
	result := policy.FilterEnv(env)

	found := false
	for _, e := range result {
		if e == "MallocStackLogging=1" {
			found = true
		}
	}
	if !found {
		t.Error("MallocStackLogging should survive when COVO_KEEP_MALLOC_DEBUG=1")
	}
}

// indexByte is a helper to avoid importing strings just for IndexByte.
func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// Ensure os import is used.
var _ = os.Environ
