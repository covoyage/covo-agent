package agent

import (
	"os"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestResolveExecMode(t *testing.T) {
	orig, hadOrig := os.LookupEnv("EXECUTION_MODE")
	defer func() {
		if hadOrig {
			os.Setenv("EXECUTION_MODE", orig)
		} else {
			os.Unsetenv("EXECUTION_MODE")
		}
	}()

	tests := []struct {
		name    string
		cfgMode string
		env     string
		setEnv  bool
		want    agentcore.ExecutionMode
	}{
		{name: "empty defaults to parallel", cfgMode: "", want: agentcore.ModeParallel},
		{name: "unknown value defaults to parallel", cfgMode: "turbo", want: agentcore.ModeParallel},
		{name: "explicit parallel", cfgMode: "parallel", want: agentcore.ModeParallel},
		{name: "explicit mixed", cfgMode: "mixed", want: agentcore.ModeMixed},
		{name: "explicit serial via config", cfgMode: "serial", want: agentcore.ModeSerial},
		{name: "serial via env", cfgMode: "", env: "serial", setEnv: true, want: agentcore.ModeSerial},
		{name: "mixed via env", cfgMode: "", env: "mixed", setEnv: true, want: agentcore.ModeMixed},
		{name: "env parallel is the default anyway", cfgMode: "", env: "parallel", setEnv: true, want: agentcore.ModeParallel},
		{name: "config wins over env", cfgMode: "mixed", env: "serial", setEnv: true, want: agentcore.ModeMixed},
		{name: "config wins over env serial", cfgMode: "parallel", env: "serial", setEnv: true, want: agentcore.ModeParallel},
		{name: "env ignored for unknown values", cfgMode: "", env: "turbo", setEnv: true, want: agentcore.ModeParallel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				os.Setenv("EXECUTION_MODE", tt.env)
			} else {
				os.Unsetenv("EXECUTION_MODE")
			}
			if got := ResolveExecMode(tt.cfgMode); got != tt.want {
				t.Errorf("ResolveExecMode(%q) = %v, want %v", tt.cfgMode, got, tt.want)
			}
		})
	}
}
