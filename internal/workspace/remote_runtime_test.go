package workspace

import "testing"

func TestRemoteRuntimeArgs(t *testing.T) {
	got := RemoteRuntimeArgs("/opt/covo-agent", "/srv/app", "acp")
	if got != "cd '/srv/app' && exec '/opt/covo-agent' acp" {
		t.Fatalf("got %q", got)
	}
}

func TestRemoteHomeUsesWorkDir(t *testing.T) {
	s := &RemoteSpec{Kind: "ssh", Host: "box", WorkDir: "/srv/app"}
	if got := s.remoteHome(); got != "/srv/app/.covo-agent-remote" {
		t.Fatalf("home=%q", got)
	}
}

func TestDisabledRemoteCannotDeploy(t *testing.T) {
	var s *RemoteSpec
	if _, err := s.DeployRuntime(nil, ""); err == nil {
		t.Fatal("nil spec should fail")
	}
}
