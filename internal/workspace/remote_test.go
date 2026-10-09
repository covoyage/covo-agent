package workspace

import "testing"

func TestParseRemote(t *testing.T) {
	spec, err := ParseRemote("alice@box:/srv/app")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Kind != "ssh" || spec.User != "alice" || spec.Host != "box" || spec.WorkDir != "/srv/app" {
		t.Fatalf("%+v", spec)
	}
	spec, err = ParseRemote("ssh://bob@host.example/work")
	if err != nil {
		t.Fatal(err)
	}
	if spec.User != "bob" || spec.Host != "host.example" || spec.WorkDir != "/work" {
		t.Fatalf("%+v", spec)
	}
	if _, err := ParseRemote(""); err == nil {
		t.Fatal("empty remote should fail")
	}
	spec, err = ParseRemote("wsl:Ubuntu:/home/me")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Kind != "wsl" || spec.Host != "Ubuntu" || spec.WorkDir != "/home/me" || !spec.Enabled() {
		t.Fatalf("%+v", spec)
	}
	spec, err = ParseRemote("docker:devbox:/src")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Kind != "docker" || spec.Host != "devbox" || spec.WorkDir != "/src" || !spec.Enabled() {
		t.Fatalf("%+v", spec)
	}
	bin, args, err := spec.transport()
	if err != nil || bin != "docker" || len(args) < 3 || args[2] != "devbox" {
		t.Fatalf("docker transport %s %v %v", bin, args, err)
	}
}
