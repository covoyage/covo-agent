package agent

import (
	"testing"
)

func TestBrowserHeadlessFromEnv(t *testing.T) {
	t.Setenv("BROWSER_HEADLESS", "")
	t.Setenv("AGENT_BROWSER_HEADED", "")
	if !browserHeadlessFromEnv() {
		t.Fatal("default should be headless")
	}

	t.Setenv("AGENT_BROWSER_HEADED", "1")
	if browserHeadlessFromEnv() {
		t.Fatal("AGENT_BROWSER_HEADED=1 should launch a visible automation window")
	}

	t.Setenv("AGENT_BROWSER_HEADED", "true")
	if browserHeadlessFromEnv() {
		t.Fatal("AGENT_BROWSER_HEADED=true should launch a visible automation window")
	}

	t.Setenv("AGENT_BROWSER_HEADED", "")
	t.Setenv("BROWSER_HEADLESS", "false")
	if browserHeadlessFromEnv() {
		t.Fatal("BROWSER_HEADLESS=false should launch a visible automation window")
	}

	t.Setenv("AGENT_BROWSER_HEADED", "1")
	t.Setenv("BROWSER_HEADLESS", "true")
	if browserHeadlessFromEnv() {
		t.Fatal("AGENT_BROWSER_HEADED should take precedence over BROWSER_HEADLESS")
	}
}
