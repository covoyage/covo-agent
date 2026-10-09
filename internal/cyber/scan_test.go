package cyber

import (
	"strings"
	"testing"
)

func TestPlanScanRecipes(t *testing.T) {
	plan, err := PlanScan(ScanRequest{Recipe: "nmap", Target: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Binary != "nmap" || len(plan.Command) < 2 {
		t.Fatalf("unexpected nmap plan: %+v", plan)
	}
	plan, err = PlanScan(ScanRequest{Recipe: "curl", Target: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Binary != "curl" {
		t.Fatalf("curl plan=%+v", plan)
	}
}

func TestPlanScanRejectsUnknown(t *testing.T) {
	if _, err := PlanScan(ScanRequest{Recipe: "msf", Target: "example.com"}); err == nil {
		t.Fatal("unknown recipe should fail")
	}
}

func TestRecipeCatalogCoversSecurityTesting(t *testing.T) {
	if len(Recipes()) < 50 {
		t.Fatalf("catalog too small: %d", len(Recipes()))
	}
	want := []string{"nmap", "nuclei", "curl", "subfinder", "amass", "rustscan", "ffuf", "katana", "sqlmap", "trivy"}
	for _, name := range want {
		if _, ok := LookupRecipe(Recipe(name)); !ok {
			t.Errorf("missing recipe %s", name)
		}
	}
	for _, blocked := range BlockedWrappers {
		if _, ok := LookupRecipe(Recipe(blocked)); ok {
			t.Errorf("blocked wrapper %s should not be a recipe", blocked)
		}
	}
}

func TestPlanScanSubfinder(t *testing.T) {
	plan, err := PlanScan(ScanRequest{Recipe: "subfinder", Target: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Binary != "subfinder" {
		t.Fatalf("plan=%+v", plan)
	}
	joined := strings.Join(plan.Command, " ")
	if !strings.Contains(joined, "-d") || !strings.Contains(joined, "example.com") {
		t.Fatalf("command=%q", joined)
	}
}

func TestHydraPlanOptions(t *testing.T) {
	plan, err := PlanScan(ScanRequest{
		Recipe: "hydra",
		Target: "10.0.0.8",
		Options: map[string]string{
			"service":       "ssh",
			"username_file": "users.txt",
			"password_file": "pass.txt",
			"tasks":         "8",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Binary != "hydra" {
		t.Fatalf("plan=%+v", plan)
	}
	joined := strings.Join(plan.Command, " ")
	for _, want := range []string{"-I", "-L users.txt", "-P pass.txt", "-f", "-t 8", "10.0.0.8", "ssh"} {
		if !strings.Contains(joined, want) {
			t.Errorf("hydra command missing %q: %s", want, joined)
		}
	}
	if strings.HasSuffix(joined, "ssh 10.0.0.8") {
		t.Errorf("service must follow target order: %s", joined)
	}
	if idxTarget := strings.Index(joined, "10.0.0.8"); idxTarget > strings.Index(joined, "ssh") {
		t.Errorf("target must come before service: %s", joined)
	}
}

func TestHydraRequiresService(t *testing.T) {
	if _, err := PlanScan(ScanRequest{Recipe: "hydra", Target: "10.0.0.8"}); err == nil {
		t.Fatal("hydra without service should fail")
	}
}

func TestMetasploitPlanOptions(t *testing.T) {
	plan, err := PlanScan(ScanRequest{
		Recipe:  "metasploit",
		Target:  "auxiliary/scanner/smb/smb_version",
		Options: map[string]string{"RHOSTS": "10.0.0.8", "Threads": "4"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Binary != "msfconsole" {
		t.Fatalf("plan=%+v", plan)
	}
	plan, err = PlanScan(ScanRequest{
		Recipe:  "metasploit",
		Target:  "auxiliary/scanner/smb/smb_version",
		Options: map[string]string{"RHOSTS": "10.0.0.8", "Threads": "4"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Binary != "msfconsole" {
		t.Fatalf("plan=%+v", plan)
	}
	x := plan.Command[len(plan.Command)-1]
	for _, want := range []string{"use auxiliary/scanner/smb/smb_version", "set RHOSTS 10.0.0.8", "set Threads 4", "run", "exit"} {
		if !strings.Contains(x, want) {
			t.Errorf("-x string missing %q: %s", want, x)
		}
	}
}

func TestScanScopeRejectsHiddenNetworkTargets(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	if _, err := catalog.SetAllowedHosts([]string{"allowed.test"}); err != nil {
		t.Fatal(err)
	}
	for _, req := range []ScanRequest{
		{Recipe: "nmap", Target: "allowed.test", Extra: []string{"-iL", "other-hosts.txt"}},
		{Recipe: "metasploit", Target: "auxiliary/scanner/smb/smb_version", Options: map[string]string{"RHOSTS": "outside.test"}},
		{Recipe: "metasploit", Target: "auxiliary/scanner/smb/smb_version", Options: map[string]string{"RHOSTS": "allowed.test", "RHOST": "outside.test"}},
		{Recipe: "metasploit", Target: "auxiliary/scanner/smb/smb_version", Options: map[string]string{"RHOSTS": "allowed.test", "PROXIES": "outside.test"}},
		{Recipe: "metasploit", Target: "auxiliary/scanner/smb/smb_version"},
		{Recipe: "fscan", Target: "allowed.test", Options: map[string]string{"host_file": "targets.txt"}},
		{Recipe: "fscan", Target: "allowed.test", Options: map[string]string{"domain": "outside.test"}},
		{Recipe: "bloodhound", Target: "allowed.test", Options: map[string]string{"dc": "outside.test"}},
		{Recipe: "impacket", Target: "impacket-secretsdump", Options: map[string]string{"args": "user@outside.test"}},
	} {
		if err := catalog.CheckScanRequestScope(req); err == nil {
			t.Fatalf("hidden target accepted: %+v", req)
		}
	}
	if err := catalog.CheckScanRequestScope(ScanRequest{Recipe: "metasploit", Target: "auxiliary/scanner/smb/smb_version", Options: map[string]string{"RHOSTS": "allowed.test"}}); err != nil {
		t.Fatal(err)
	}
}

func TestMetasploitRejectsBareTarget(t *testing.T) {
	if _, err := PlanScan(ScanRequest{Recipe: "metasploit", Target: "smb_version"}); err == nil {
		t.Fatal("metasploit without module path should fail")
	}
}

// msfconsole re-splits its -x resource string, so a separator in any part of
// the command would run as an extra console command.
func TestMetasploitRejectsCommandSeparators(t *testing.T) {
	cases := []struct {
		name    string
		target  string
		options map[string]string
	}{
		{"value semicolon", "auxiliary/scanner/smb/smb_version", map[string]string{"RHOSTS": "10.0.0.8; run exploit/windows/smb/psexec"}},
		{"value newline", "auxiliary/scanner/smb/smb_version", map[string]string{"LHOST": "10.0.0.5\nset LPORT 4444"}},
		{"key semicolon", "auxiliary/scanner/smb/smb_version", map[string]string{"RHOSTS; setg FRIDGE": "10.0.0.8"}},
		{"target semicolon", "auxiliary/scanner/smb/smb_version; run exploit/multi/handler", nil},
	}
	for _, tc := range cases {
		if _, err := PlanScan(ScanRequest{Recipe: "metasploit", Target: tc.target, Options: tc.options}); err == nil {
			t.Errorf("%s: expected rejection, got a plan", tc.name)
		}
	}
}

func TestMsfvenomPlanOptions(t *testing.T) {
	plan, err := PlanScan(ScanRequest{
		Recipe: "msfvenom",
		Target: "windows/x64/meterpreter/reverse_tcp",
		Options: map[string]string{
			"format": "exe",
			"output": "payload.exe",
			"LHOST":  "10.0.0.1",
			"LPORT":  "4444",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Binary != "msfvenom" {
		t.Fatalf("plan=%+v", plan)
	}
	joined := strings.Join(plan.Command, " ")
	for _, want := range []string{"-p windows/x64/meterpreter/reverse_tcp", "-f exe", "-o payload.exe", "LHOST=10.0.0.1", "LPORT=4444"} {
		if !strings.Contains(joined, want) {
			t.Errorf("msfvenom command missing %q: %s", want, joined)
		}
	}
}

func TestResponderPlanOptions(t *testing.T) {
	plan, err := PlanScan(ScanRequest{
		Recipe:  "responder",
		Target:  "eth0",
		Options: map[string]string{"analyze": "true", "wpad": "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan.Command, " ")
	for _, want := range []string{"-I eth0", "-A", "-w"} {
		if !strings.Contains(joined, want) {
			t.Errorf("responder command missing %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, " -f") {
		t.Errorf("fingerprint should stay off: %s", joined)
	}
}

func TestImpacketPlanOptions(t *testing.T) {
	plan, err := PlanScan(ScanRequest{
		Recipe:  "impacket",
		Target:  "/usr/share/doc/python3-impacket/examples/secretsdump.py",
		Options: map[string]string{"args": "user:pass@10.0.0.8"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Binary != "python3" {
		t.Fatalf(".py script should run via python3, got %s", plan.Binary)
	}
	joined := strings.Join(plan.Command, " ")
	if !strings.Contains(joined, "secretsdump.py user:pass@10.0.0.8") {
		t.Errorf("command=%q", joined)
	}

	plan, err = PlanScan(ScanRequest{Recipe: "impacket", Target: "impacket-secretsdump"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Binary != "impacket-secretsdump" {
		t.Fatalf("packaged entrypoint should be the binary, got %s", plan.Binary)
	}
}

func TestNetexecPlanOptions(t *testing.T) {
	plan, err := PlanScan(ScanRequest{
		Recipe: "netexec",
		Target: "10.0.0.8",
		Options: map[string]string{
			"protocol": "smb",
			"username": "admin",
			"hash":     "aad3b435b51404ee",
			"module":   "enum_dns",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan.Command, " ")
	for _, want := range []string{"smb 10.0.0.8", "-u admin", "-H aad3b435b51404ee", "-M enum_dns"} {
		if !strings.Contains(joined, want) {
			t.Errorf("netexec command missing %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "-p") {
		t.Errorf("password should not appear when only hash given: %s", joined)
	}
}

func TestHashcatPlanOptions(t *testing.T) {
	if _, err := PlanScan(ScanRequest{Recipe: "hashcat", Target: "hashes.txt"}); err == nil {
		t.Fatal("hashcat without hash_type should fail")
	}
	plan, err := PlanScan(ScanRequest{
		Recipe:  "hashcat",
		Target:  "hashes.txt",
		Options: map[string]string{"hash_type": "1000"},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan.Command, " ")
	for _, want := range []string{"-m 1000", "-a 0", "hashes.txt", "rockyou.txt"} {
		if !strings.Contains(joined, want) {
			t.Errorf("hashcat command missing %q: %s", want, joined)
		}
	}
}

func TestJohnPlanOptions(t *testing.T) {
	plan, err := PlanScan(ScanRequest{
		Recipe:  "john",
		Target:  "hashes.txt",
		Options: map[string]string{"format": "NT"},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan.Command, " ")
	for _, want := range []string{"--wordlist=/usr/share/wordlists/rockyou.txt", "--format=NT", "hashes.txt"} {
		if !strings.Contains(joined, want) {
			t.Errorf("john command missing %q: %s", want, joined)
		}
	}
}

func TestFscanAndBloodhoundAndLinpeas(t *testing.T) {
	plan, err := PlanScan(ScanRequest{
		Recipe:  "fscan",
		Target:  "192.168.1.0/24",
		Options: map[string]string{"ports": "22,80,445", "username": "root", "password": "toor"},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan.Command, " ")
	for _, want := range []string{"-h 192.168.1.0/24", "-p 22,80,445", "-user root", "-pwd toor"} {
		if !strings.Contains(joined, want) {
			t.Errorf("fscan command missing %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "-rsh") {
		t.Errorf("fscan must not expose reverse-shell flag: %s", joined)
	}

	plan, err = PlanScan(ScanRequest{
		Recipe:  "bloodhound",
		Target:  "corp.local",
		Options: map[string]string{"username": "svc", "password": "pw"},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(plan.Command, " ")
	for _, want := range []string{"-d corp.local", "-u svc", "-p pw", "-c All"} {
		if !strings.Contains(joined, want) {
			t.Errorf("bloodhound command missing %q: %s", want, joined)
		}
	}

	plan, err = PlanScan(ScanRequest{Recipe: "linpeas", Target: "local", Options: map[string]string{"mode": "all"}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Binary != "linpeas.sh" || strings.Join(plan.Command, " ") != "linpeas.sh -a" {
		t.Fatalf("linpeas plan=%+v", plan)
	}
}

func TestScriptRunnerRecipes(t *testing.T) {
	plan, err := PlanScan(ScanRequest{
		Recipe: "pwntools",
		Target: "/path/service",
		Options: map[string]string{
			"script": "from pwn import *",
			"host":   "10.0.0.8",
			"port":   "1337",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Binary != "python3" {
		t.Fatalf("binary=%s", plan.Binary)
	}
	if plan.Command[1] != "-c" || plan.Command[3] != "from pwn import *" {
		t.Fatalf("command=%v", plan.Command)
	}
	if !strings.Contains(plan.Command[2], "PWN_HOST") {
		t.Errorf("prelude must set PWN_* env")
	}

	plan, err = PlanScan(ScanRequest{
		Recipe:  "angr",
		Target:  "/bin/crackme",
		Options: map[string]string{"script": "import angr", "find_address": "0x401000"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Command[2], "binary_path") {
		t.Errorf("angr prelude must inject binary_path")
	}

	if _, err := PlanScan(ScanRequest{Recipe: "pwntools", Target: "x"}); err == nil {
		t.Fatal("pwntools without script should fail")
	}
	if _, err := PlanScan(ScanRequest{Recipe: "angr", Target: ""}); err == nil {
		t.Fatal("angr without target should fail")
	}
}

func TestHashpumpXsserDotdotpwnGhidraPacu(t *testing.T) {
	if _, err := PlanScan(ScanRequest{Recipe: "hashpump", Target: "d"}); err == nil {
		t.Fatal("hashpump missing options should fail")
	}
	plan, err := PlanScan(ScanRequest{
		Recipe:  "hashpump",
		Target:  "data",
		Options: map[string]string{"signature": "abc", "key_length": "8", "append": "admin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan.Command, " ")
	for _, want := range []string{"-s abc", "-d data", "-k 8", "-a admin"} {
		if !strings.Contains(joined, want) {
			t.Errorf("hashpump missing %q: %s", want, joined)
		}
	}

	plan, err = PlanScan(ScanRequest{Recipe: "xsser", Target: "https://x.com/s"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan.Command, " "), "--url https://x.com/s") {
		t.Errorf("xsser=%v", plan.Command)
	}

	plan, err = PlanScan(ScanRequest{Recipe: "dotdotpwn", Target: "10.0.0.8"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plan.Command, " ") != "dotdotpwn -m http -h 10.0.0.8" {
		t.Errorf("dotdotpwn=%v", plan.Command)
	}

	plan, err = PlanScan(ScanRequest{
		Recipe:  "ghidra",
		Target:  "/bin/app",
		Options: map[string]string{"script": "Decompile.java"},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(plan.Command, " ")
	for _, want := range []string{"analyzeHeadless /tmp/ghidra_projects covo_analysis -import /bin/app -postScript Decompile.java"} {
		if !strings.Contains(joined, want) {
			t.Errorf("ghidra missing %q: %s", want, joined)
		}
	}

	plan, err = PlanScan(ScanRequest{
		Recipe:  "pacu",
		Target:  "aws-acct",
		Options: map[string]string{"modules": "enum__users"},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(plan.Command, " ")
	if !strings.Contains(joined, "--session covo_session") || !strings.Contains(joined, "--modules enum__users") {
		t.Errorf("pacu=%s", joined)
	}
}
