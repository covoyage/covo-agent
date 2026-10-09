package cyber

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Recipe string

const (
	RecipeNmap   Recipe = "nmap"
	RecipeNuclei Recipe = "nuclei"
	RecipeCurl   Recipe = "curl"
	RecipeList   Recipe = "list"
)

type TargetKind string

const (
	TargetHost   TargetKind = "host"
	TargetURL    TargetKind = "url"
	TargetFile   TargetKind = "file"
	TargetQuery  TargetKind = "query"
	TargetModule TargetKind = "module"
	TargetLocal  TargetKind = "local"
)

// RecipeSpec is one argv-pinned external-tool wrapper exposed via scan.
//
// The recipe selection and the option naming of the argument builders below
// were modeled on the upstream project's recipe catalog; the argv is constructed
// natively here.
type RecipeSpec struct {
	Name        Recipe
	Binary      string
	Summary     string
	TargetKind  TargetKind
	Timeout     time.Duration
	AllowedExit []int
	Args        func(target string, extra []string) ([]string, error)
	// BinaryFor optionally derives the executable from the scan target,
	// e.g. impacket runs .py scripts via python3 but packaged
	// impacket-* entrypoints directly. When nil, Binary is used.
	BinaryFor func(target string) string
	// OptionsArgs builds args from named options instead of plain extra
	// flags. When set, the scan tool's "options" object is passed here.
	OptionsArgs func(target string, options map[string]string, extra []string) ([]string, error)
}

var (
	recipeCatalog []RecipeSpec
	recipeByName  map[Recipe]RecipeSpec
)

func init() {
	recipeCatalog = buildRecipeCatalog()
	recipeByName = make(map[Recipe]RecipeSpec, len(recipeCatalog))
	for _, spec := range recipeCatalog {
		recipeByName[spec.Name] = spec
	}
}

// Recipes returns the scan recipe catalog in definition order.
func Recipes() []RecipeSpec {
	out := make([]RecipeSpec, len(recipeCatalog))
	copy(out, recipeCatalog)
	return out
}

// RecipeNames returns sorted recipe names, including "list".
func RecipeNames() []string {
	names := make([]string, 0, len(recipeCatalog)+1)
	names = append(names, "list")
	for _, spec := range recipeCatalog {
		names = append(names, string(spec.Name))
	}
	sort.Strings(names)
	return names
}

// RecipeBinaries returns unique binaries used by the catalog.
func RecipeBinaries() []string {
	seen := make(map[string]bool, len(recipeCatalog))
	out := make([]string, 0, len(recipeCatalog))
	for _, spec := range recipeCatalog {
		if seen[spec.Binary] {
			continue
		}
		seen[spec.Binary] = true
		out = append(out, spec.Binary)
	}
	sort.Strings(out)
	return out
}

func LookupRecipe(name Recipe) (RecipeSpec, bool) {
	spec, ok := recipeByName[Recipe(strings.ToLower(strings.TrimSpace(string(name))))]
	return spec, ok
}

func RecipeSummaries() []map[string]string {
	out := make([]map[string]string, 0, len(recipeCatalog))
	for _, spec := range recipeCatalog {
		out = append(out, map[string]string{
			"recipe":  string(spec.Name),
			"binary":  spec.Binary,
			"target":  string(spec.TargetKind),
			"summary": spec.Summary,
		})
	}
	return out
}

func buildRecipeCatalog() []RecipeSpec {
	return []RecipeSpec{
		// --- host / port recon ---
		{Name: "nmap", Binary: "nmap", Summary: "Port/service scan", TargetKind: TargetHost, Timeout: 3 * time.Minute, Args: positional([]string{"-Pn", "-sV", "--top-ports", "1000"})},
		{Name: "rustscan", Binary: "rustscan", Summary: "Fast port discovery", TargetKind: TargetHost, Timeout: 3 * time.Minute, Args: flagThen([]string{"-a"}, []string{"--ulimit", "5000", "-g", "--scripts", "none"})},
		{Name: "masscan", Binary: "masscan", Summary: "High-speed port scan (rate-limited)", TargetKind: TargetHost, Timeout: 3 * time.Minute, Args: positional([]string{"-p", "1-1000", "--rate", "1000"})},
		{Name: "arp-scan", Binary: "arp-scan", Summary: "Local ARP host discovery", TargetKind: TargetHost, Timeout: 2 * time.Minute, Args: positional(nil)},
		{Name: "nbtscan", Binary: "nbtscan", Summary: "NetBIOS name scan", TargetKind: TargetHost, Timeout: 2 * time.Minute, Args: positional(nil)},
		{Name: "curl", Binary: "curl", Summary: "HTTP fingerprint", TargetKind: TargetURL, Timeout: 1 * time.Minute, Args: flagThen([]string{"--url"}, []string{"-sS", "--max-redirs", "0", "--max-time", "20"})},

		// --- DNS / subdomain ---
		{Name: "subfinder", Binary: "subfinder", Summary: "Passive subdomain discovery", TargetKind: TargetHost, Timeout: 3 * time.Minute, Args: flagThen([]string{"-d"}, []string{"-silent"})},
		{Name: "amass", Binary: "amass", Summary: "Passive subdomain enum", TargetKind: TargetHost, Timeout: 5 * time.Minute, Args: flagThen([]string{"-d"}, []string{"enum", "-passive", "-silent"})},
		{Name: "dnsenum", Binary: "dnsenum", Summary: "DNS enumeration", TargetKind: TargetHost, Timeout: 3 * time.Minute, Args: positional([]string{"--noreverse"})},
		{Name: "fierce", Binary: "fierce", Summary: "DNS reconnaissance", TargetKind: TargetHost, Timeout: 3 * time.Minute, Args: flagThen([]string{"--domain"}, nil)},

		// --- URL / content discovery ---
		{Name: "katana", Binary: "katana", Summary: "Web crawler / endpoint discovery", TargetKind: TargetURL, Timeout: 3 * time.Minute, Args: flagThen([]string{"-u"}, []string{"-d", "2", "-silent"})},
		{Name: "gau", Binary: "gau", Summary: "Known URLs from public sources", TargetKind: TargetHost, Timeout: 3 * time.Minute, Args: positional([]string{"--subs"})},
		{Name: "waybackurls", Binary: "waybackurls", Summary: "Historical URLs from Wayback", TargetKind: TargetHost, Timeout: 3 * time.Minute, Args: positional(nil)},
		{Name: "paramspider", Binary: "paramspider", Summary: "Parameter discovery from archives", TargetKind: TargetHost, Timeout: 3 * time.Minute, Args: flagThen([]string{"-d"}, nil)},
		{Name: "ffuf", Binary: "ffuf", Summary: "Web content / directory fuzzing", TargetKind: TargetURL, Timeout: 3 * time.Minute, Args: ffufArgs},
		{Name: "gobuster", Binary: "gobuster", Summary: "Directory / DNS / vhost busting", TargetKind: TargetURL, Timeout: 3 * time.Minute, Args: gobusterArgs},
		{Name: "feroxbuster", Binary: "feroxbuster", Summary: "Recursive content discovery", TargetKind: TargetURL, Timeout: 3 * time.Minute, Args: wordlistFlag("-u", "-w", []string{"-q", "-t", "20", "--no-state"})},
		{Name: "dirsearch", Binary: "dirsearch", Summary: "Web path discovery", TargetKind: TargetURL, Timeout: 3 * time.Minute, Args: wordlistFlag("-u", "-w", []string{"-q", "-e", "php,html,js,txt"})},
		{Name: "arjun", Binary: "arjun", Summary: "HTTP parameter discovery", TargetKind: TargetURL, Timeout: 3 * time.Minute, Args: flagThen([]string{"-u"}, []string{"--stable"})},
		{Name: "x8", Binary: "x8", Summary: "Hidden parameter discovery", TargetKind: TargetURL, Timeout: 3 * time.Minute, Args: wordlistFlag("-u", "-w", nil)},
		{Name: "wafw00f", Binary: "wafw00f", Summary: "WAF fingerprint", TargetKind: TargetURL, Timeout: 2 * time.Minute, Args: positional(nil)},

		// --- SMB / Windows recon ---
		{Name: "smbmap", Binary: "smbmap", Summary: "SMB share enumeration", TargetKind: TargetHost, Timeout: 3 * time.Minute, Args: flagThen([]string{"-H"}, nil)},
		{Name: "enum4linux-ng", Binary: "enum4linux-ng", Summary: "SMB / Windows enumeration", TargetKind: TargetHost, Timeout: 3 * time.Minute, Args: positional([]string{"-A"})},
		{Name: "rpcclient", Binary: "rpcclient", Summary: "MSRPC info query", TargetKind: TargetHost, Timeout: 2 * time.Minute, Args: rpcclientArgs},

		// --- local source / secret / dependency floor ---
		{Name: "semgrep", Binary: "semgrep", Summary: "Static rules against a local tree", TargetKind: TargetFile, Timeout: 5 * time.Minute, Args: positional([]string{"scan", "--config", "auto", "--quiet", "--error"})},
		{Name: "gitleaks", Binary: "gitleaks", Summary: "Secret scan of a local tree", TargetKind: TargetFile, Timeout: 3 * time.Minute, Args: positional([]string{"dir", "--no-banner", "--redact"})},
		{Name: "trufflehog", Binary: "trufflehog", Summary: "Secret scan of a local filesystem", TargetKind: TargetFile, Timeout: 3 * time.Minute, Args: positional([]string{"filesystem", "--no-update"})},

		// --- OSINT APIs (CLI if installed) ---
		{Name: "shodan_search", Binary: "shodan", Summary: "Shodan host/query search", TargetKind: TargetQuery, Timeout: 2 * time.Minute, Args: positional([]string{"search", "--limit", "20"})},
		{Name: "fofa_search", Binary: "fofa", Summary: "FOFA asset search", TargetKind: TargetQuery, Timeout: 2 * time.Minute, Args: positional([]string{"search"})},
		{Name: "quake_search", Binary: "quake", Summary: "Quake asset search", TargetKind: TargetQuery, Timeout: 2 * time.Minute, Args: positional([]string{"search"})},
		{Name: "zoomeye_search", Binary: "zoomeye", Summary: "ZoomEye asset search", TargetKind: TargetQuery, Timeout: 2 * time.Minute, Args: positional([]string{"search"})},
		{Name: "virustotal_search", Binary: "vt", Summary: "VirusTotal lookup", TargetKind: TargetQuery, Timeout: 2 * time.Minute, Args: positional([]string{"search"})},

		// --- verify / vuln scanners ---
		{Name: "nuclei", Binary: "nuclei", Summary: "Template vulnerability scan", TargetKind: TargetURL, Timeout: 5 * time.Minute, Args: flagThen([]string{"-u"}, []string{"-severity", "low,medium,high,critical", "-silent"})},
		{Name: "nikto", Binary: "nikto", Summary: "Web server misconfig scan", TargetKind: TargetURL, Timeout: 5 * time.Minute, AllowedExit: []int{1}, Args: flagThen([]string{"-h"}, []string{"-ask", "no", "-maxtime", "60s"})},
		{Name: "dalfox", Binary: "dalfox", Summary: "XSS verification", TargetKind: TargetURL, Timeout: 5 * time.Minute, Args: positional([]string{"url", "--silence", "--skip-bav"})},
		{Name: "wpscan", Binary: "wpscan", Summary: "WordPress vulnerability scan", TargetKind: TargetURL, Timeout: 5 * time.Minute, AllowedExit: []int{4}, Args: flagThen([]string{"--url"}, []string{"--no-banner", "--random-user-agent"})},
		{Name: "jaeles", Binary: "jaeles", Summary: "Signature-based web scan", TargetKind: TargetURL, Timeout: 5 * time.Minute, Args: flagThen([]string{"-u"}, []string{"scan"})},
		{Name: "zap", Binary: "zap-cli", Summary: "OWASP ZAP baseline scan", TargetKind: TargetURL, Timeout: 5 * time.Minute, Args: positional([]string{"quick-scan"})},
		{Name: "graphql-scanner", Binary: "graphqlmap", Summary: "GraphQL introspection / checks", TargetKind: TargetURL, Timeout: 3 * time.Minute, Args: flagThen([]string{"-u"}, nil)},
		{Name: "jwt-analyzer", Binary: "jwt_tool", Summary: "JWT token analysis", TargetKind: TargetQuery, Timeout: 1 * time.Minute, Args: positional(nil)},
		{Name: "api-schema-analyzer", Binary: "spectral", Summary: "OpenAPI/Swagger lint", TargetKind: TargetFile, Timeout: 2 * time.Minute, Args: positional([]string{"lint"})},
		{Name: "kube-hunter", Binary: "kube-hunter", Summary: "Kubernetes remote recon", TargetKind: TargetHost, Timeout: 3 * time.Minute, Args: flagThen([]string{"--remote"}, []string{"--report", "json"})},

		// --- exploit-in-scope detection (no C2 / shell) ---
		{Name: "sqlmap", Binary: "sqlmap", Summary: "SQLi detection (batch, no shell)", TargetKind: TargetURL, Timeout: 5 * time.Minute, Args: flagThen([]string{"-u"}, []string{"--batch", "--smart", "--level=1", "--risk=1"})},

		// --- attack / exploitation (authorized targets only) ---
		{Name: "hydra", Binary: "hydra", Summary: "Network login brute force (options: service required, username/-l, username_file/-L, password/-p, password_file/-P, tasks, wait, output)", TargetKind: TargetHost, Timeout: 10 * time.Minute, OptionsArgs: hydraArgs},
		{Name: "metasploit", Binary: "msfconsole", Summary: "Run a Metasploit module non-interactively; target is the module path, options become set commands (e.g. RHOSTS)", TargetKind: TargetModule, Timeout: 10 * time.Minute, OptionsArgs: metasploitArgs},
		{Name: "msfvenom", Binary: "msfvenom", Summary: "Generate a Metasploit payload; target is the payload path, options: format, output, encoder, iterations, plus payload vars (LHOST, LPORT)", TargetKind: TargetModule, Timeout: 5 * time.Minute, OptionsArgs: msfvenomArgs},
		{Name: "responder", Binary: "responder", Summary: "LLMNR/NBT-NS/MDNS poisoner and credential capture; target is the interface (default eth0), options: analyze, wpad, fingerprint", TargetKind: TargetLocal, Timeout: 5 * time.Minute, OptionsArgs: responderArgs},
		{Name: "impacket", Binary: "python3", Summary: "Run an Impacket script (secretsdump.py, psexec.py, wmiexec.py, ...); target is the script path or packaged name (impacket-secretsdump), options: args (space-separated script arguments)", TargetKind: TargetModule, Timeout: 10 * time.Minute, BinaryFor: impacketBinary, OptionsArgs: impacketArgs},
		{Name: "netexec", Binary: "netexec", Summary: "Network enumeration/exploitation over smb/ssh/winrm/ldap (options: protocol, username, password, hash, module)", TargetKind: TargetHost, Timeout: 10 * time.Minute, OptionsArgs: netexecArgs},
		{Name: "fscan", Binary: "fscan", Summary: "One-stop intranet scan: hosts, ports, services, weak passwords, POCs (options: ports, mode, url, cookie, username, password, user_file, pass_file, host_file, pocname, proxy; extra -nobr -nopoc to limit scope)", TargetKind: TargetHost, Timeout: 10 * time.Minute, OptionsArgs: fscanArgs},
		{Name: "bloodhound", Binary: "bloodhound-python", Summary: "Active Directory attack-path data collection; target is the domain (options: username, password, collection, dc)", TargetKind: TargetHost, Timeout: 15 * time.Minute, OptionsArgs: bloodhoundArgs},
		{Name: "linpeas", Binary: "linpeas.sh", Summary: "Linux privilege-escalation enumeration on this host (options: mode=all|fast|superfast)", TargetKind: TargetLocal, Timeout: 10 * time.Minute, OptionsArgs: linpeasArgs},
		{Name: "hashcat", Binary: "hashcat", Summary: "GPU-accelerated password recovery; target is the hash file (options: hash_type required, attack_mode, wordlist, mask)", TargetKind: TargetFile, Timeout: 30 * time.Minute, OptionsArgs: hashcatArgs},
		{Name: "john", Binary: "john", Summary: "John the Ripper password cracking; target is the hash file (options: wordlist, format)", TargetKind: TargetFile, Timeout: 30 * time.Minute, OptionsArgs: johnArgs},
		{Name: "xsser", Binary: "xsser", Summary: "Automated XSS testing framework", TargetKind: TargetURL, Timeout: 5 * time.Minute, Args: flagThen([]string{"--url"}, nil)},
		{Name: "dotdotpwn", Binary: "dotdotpwn", Summary: "Traversal fuzzing (HTTP/FTP/TFTP); target is the host (options: module, default http)", TargetKind: TargetHost, Timeout: 10 * time.Minute, OptionsArgs: dotdotpwnArgs},
		{Name: "hashpump", Binary: "hashpump", Summary: "Hash length extension attack; target is the original data (options: signature, key_length, append — all required)", TargetKind: TargetQuery, Timeout: 1 * time.Minute, OptionsArgs: hashpumpArgs},
		{Name: "pacu", Binary: "pacu", Summary: "AWS exploitation framework; target is a session label (options: session, modules, regions)", TargetKind: TargetQuery, Timeout: 10 * time.Minute, OptionsArgs: pacuArgs},
		{Name: "pwntools", Binary: "python3", Summary: "Run a pwntools exploit script; target is a local binary or host label (options: script required, binary, host, port, exploit_type → PWN_* env)", TargetKind: TargetFile, Timeout: 5 * time.Minute, BinaryFor: python3Always, OptionsArgs: pwntoolsArgs},
		{Name: "angr", Binary: "python3", Summary: "Run an angr symbolic-analysis script; target is the binary path (options: script required, find_address, avoid_addresses, analysis_type)", TargetKind: TargetFile, Timeout: 10 * time.Minute, BinaryFor: python3Always, OptionsArgs: angrArgs},
		{Name: "ghidra", Binary: "analyzeHeadless", Summary: "Ghidra headless analysis; target is the binary (options: project_dir, project_name, script)", TargetKind: TargetFile, Timeout: 15 * time.Minute, OptionsArgs: ghidraArgs},

		// --- cloud / IaC / containers ---
		{Name: "trivy", Binary: "trivy", Summary: "Image or filesystem CVE scan", TargetKind: TargetFile, Timeout: 5 * time.Minute, Args: trivyArgs},
		{Name: "checkov", Binary: "checkov", Summary: "IaC misconfig scan", TargetKind: TargetFile, Timeout: 3 * time.Minute, Args: flagThen([]string{"-d"}, []string{"--quiet", "--compact"})},
		{Name: "terrascan", Binary: "terrascan", Summary: "IaC policy scan", TargetKind: TargetFile, Timeout: 3 * time.Minute, Args: flagThen([]string{"-d"}, []string{"scan"})},
		{Name: "kube-bench", Binary: "kube-bench", Summary: "CIS Kubernetes benchmark", TargetKind: TargetFile, Timeout: 3 * time.Minute, Args: kubeBenchArgs},
		{Name: "prowler", Binary: "prowler", Summary: "Cloud security assessment", TargetKind: TargetQuery, Timeout: 5 * time.Minute, Args: prowlerArgs},
		{Name: "scout-suite", Binary: "scout", Summary: "Cloud configuration audit", TargetKind: TargetQuery, Timeout: 5 * time.Minute, Args: scoutArgs},

		// --- binary / forensic (local files) ---
		{Name: "strings", Binary: "strings", Summary: "Extract printable strings", TargetKind: TargetFile, Timeout: 1 * time.Minute, Args: positional([]string{"-n", "8"})},
		{Name: "xxd", Binary: "xxd", Summary: "Hex dump (first 512 bytes)", TargetKind: TargetFile, Timeout: 1 * time.Minute, Args: xxdArgs},
		{Name: "exiftool", Binary: "exiftool", Summary: "File metadata extraction", TargetKind: TargetFile, Timeout: 1 * time.Minute, Args: positional(nil)},
		{Name: "binwalk", Binary: "binwalk", Summary: "Firmware / embedded file analysis", TargetKind: TargetFile, Timeout: 3 * time.Minute, Args: positional(nil)},
		{Name: "steghide", Binary: "steghide", Summary: "Stego metadata probe", TargetKind: TargetFile, Timeout: 1 * time.Minute, Args: positional([]string{"info"})},
		{Name: "zsteg", Binary: "zsteg", Summary: "PNG/BMP stego detection", TargetKind: TargetFile, Timeout: 2 * time.Minute, Args: positional(nil)},
		{Name: "objdump", Binary: "objdump", Summary: "ELF/PE section dump", TargetKind: TargetFile, Timeout: 1 * time.Minute, Args: positional([]string{"-h"})},
		{Name: "checksec", Binary: "checksec", Summary: "Binary hardening checks", TargetKind: TargetFile, Timeout: 1 * time.Minute, Args: checksecArgs},
		{Name: "radare2", Binary: "r2", Summary: "Binary info via radare2", TargetKind: TargetFile, Timeout: 2 * time.Minute, Args: flagThen(nil, []string{"-qq", "-c", "iI"})},
		{Name: "gdb", Binary: "gdb", Summary: "Batch binary file info", TargetKind: TargetFile, Timeout: 1 * time.Minute, Args: gdbArgs},
		{Name: "volatility3", Binary: "vol", Summary: "Memory image info", TargetKind: TargetFile, Timeout: 3 * time.Minute, Args: volArgs},
		{Name: "foremost", Binary: "foremost", Summary: "File carving from images", TargetKind: TargetFile, Timeout: 3 * time.Minute, Args: positional([]string{"-t", "all", "-i"})},
		{Name: "ropgadget", Binary: "ROPgadget", Summary: "ROP gadget search", TargetKind: TargetFile, Timeout: 2 * time.Minute, Args: flagThen([]string{"--binary"}, nil)},
		{Name: "ropper", Binary: "ropper", Summary: "ROP/JOP gadget search", TargetKind: TargetFile, Timeout: 2 * time.Minute, Args: flagThen([]string{"--file"}, []string{"--nocolor"})},
		{Name: "one-gadget", Binary: "one_gadget", Summary: "libc one-gadget search", TargetKind: TargetFile, Timeout: 1 * time.Minute, Args: positional(nil)},
		{Name: "libc-database", Binary: "libc-database", Summary: "libc identification / offsets", TargetKind: TargetQuery, Timeout: 1 * time.Minute, Args: positional([]string{"find"})},
		{Name: "pwninit", Binary: "pwninit", Summary: "CTF binary setup", TargetKind: TargetFile, Timeout: 2 * time.Minute, Args: flagThen([]string{"--bin"}, nil)},
		{Name: "clair", Binary: "clairctl", Summary: "Container image CVE report", TargetKind: TargetQuery, Timeout: 3 * time.Minute, Args: positional([]string{"report"})},
		{Name: "falco", Binary: "falco", Summary: "Runtime security rules validate", TargetKind: TargetFile, Timeout: 1 * time.Minute, Args: falcoArgs},
		{Name: "cloudmapper", Binary: "cloudmapper", Summary: "AWS collect/report", TargetKind: TargetQuery, Timeout: 5 * time.Minute, Args: positional([]string{"collect"})},
	}
}

// BlockedWrappers are catalog candidates deliberately kept out of the default
// recipe set: unconstrained shell/python wrappers, generic package installers,
// and outbound-exfiltration helpers. Two reasons they stay out:
//  1. They defeat the approval gate's granularity — an arbitrary-command
//     wrapper makes every per-recipe policy below meaningless, and covo-agent
//     already covers that capability natively via bash / execute_code / run_code,
//     which are separately gated and audited.
//  2. They are delivery/infrastructure helpers rather than technique recipes;
//     a scanner argv has a reviewable blast radius, a shell escape hatch does not.
//
// The exploitation arsenal (metasploit, msfvenom, hydra, responder, impacket,
// netexec, fscan, bloodhound, linpeas, hashcat, john) stays in as first-class
// recipes.
var BlockedWrappers = []string{
	"dnslog", "http-framework-test", "lightx",
	"exec", "execute-python-script", "install-python-package",
	"query-execution-result",
}

func positional(prefix []string) func(string, []string) ([]string, error) {
	return func(target string, extra []string) ([]string, error) {
		args := append(append([]string{}, prefix...), target)
		return appendExtra(args, extra), nil
	}
}

func flagThen(flagAndTarget []string, prefix []string) func(string, []string) ([]string, error) {
	return func(target string, extra []string) ([]string, error) {
		args := append([]string{}, prefix...)
		args = append(args, flagAndTarget...)
		args = append(args, target)
		return appendExtra(args, extra), nil
	}
}

func wordlistFlag(targetFlag, wordFlag string, prefix []string) func(string, []string) ([]string, error) {
	return func(target string, extra []string) ([]string, error) {
		args := append([]string{}, prefix...)
		if targetFlag != "" {
			args = append(args, targetFlag, target)
		} else {
			args = append(args, target)
		}
		if !hasToken(extra, wordFlag) {
			wl, err := defaultWordlist()
			if err != nil {
				return nil, err
			}
			args = append(args, wordFlag, wl)
		}
		return appendExtra(args, extra), nil
	}
}

func ffufArgs(target string, extra []string) ([]string, error) {
	if !strings.Contains(target, "FUZZ") {
		if strings.HasSuffix(target, "/") {
			target += "FUZZ"
		} else {
			target += "/FUZZ"
		}
	}
	args := []string{"-u", target, "-mc", "200,204,301,302,307,401,403", "-t", "20"}
	if !hasToken(extra, "-w") {
		wl, err := defaultWordlist()
		if err != nil {
			return nil, err
		}
		args = append(args, "-w", wl)
	}
	return appendExtra(args, extra), nil
}

func gobusterArgs(target string, extra []string) ([]string, error) {
	args := []string{"dir", "-u", target, "-q", "-t", "20", "--no-error"}
	if !hasToken(extra, "-w") {
		wl, err := defaultWordlist()
		if err != nil {
			return nil, err
		}
		args = append(args, "-w", wl)
	}
	return appendExtra(args, extra), nil
}

func rpcclientArgs(target string, extra []string) ([]string, error) {
	args := []string{"-U", "%", target, "-c", "srvinfo"}
	return appendExtra(args, extra), nil
}

func trivyArgs(target string, extra []string) ([]string, error) {
	// A local directory is a filesystem scan; anything else is treated as an image ref.
	kind := "image"
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		kind = "fs"
	}
	args := []string{kind, target, "--quiet", "--scanners", "vuln"}
	return appendExtra(args, extra), nil
}

func kubeBenchArgs(target string, extra []string) ([]string, error) {
	_ = target
	args := []string{"run"}
	return appendExtra(args, extra), nil
}

func prowlerArgs(target string, extra []string) ([]string, error) {
	args := []string{"aws", "--brief"}
	if strings.TrimSpace(target) != "" && strings.ToLower(target) != "aws" {
		args = []string{target, "--brief"}
	}
	return appendExtra(args, extra), nil
}

func scoutArgs(target string, extra []string) ([]string, error) {
	provider := strings.TrimSpace(target)
	if provider == "" {
		provider = "aws"
	}
	args := []string{provider, "--no-browser"}
	return appendExtra(args, extra), nil
}

func xxdArgs(target string, extra []string) ([]string, error) {
	args := []string{"-l", "512", target}
	return appendExtra(args, extra), nil
}

func checksecArgs(target string, extra []string) ([]string, error) {
	args := []string{"--file=" + target}
	return appendExtra(args, extra), nil
}

func gdbArgs(target string, extra []string) ([]string, error) {
	args := []string{"-batch", "-ex", "info file", "-ex", "quit", target}
	return appendExtra(args, extra), nil
}

func volArgs(target string, extra []string) ([]string, error) {
	args := []string{"-f", target, "windows.info"}
	return appendExtra(args, extra), nil
}

func falcoArgs(target string, extra []string) ([]string, error) {
	args := []string{"--validate", target}
	return appendExtra(args, extra), nil
}

func defaultWordlist() (string, error) {
	candidates := []string{
		"/usr/share/seclists/Discovery/Web-Content/common.txt",
		"/usr/share/wordlists/dirb/common.txt",
		"/usr/share/wordlists/dirbuster/directory-list-2.3-small.txt",
		"/opt/homebrew/share/wordlists/dirb/common.txt",
	}
	for _, path := range candidates {
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("no content-discovery wordlist found; pass extra [\"-w\", \"/path/to/wordlist\"]")
}

func hasToken(extra []string, flag string) bool {
	for _, e := range extra {
		if strings.TrimSpace(e) == flag {
			return true
		}
		if strings.HasPrefix(strings.TrimSpace(e), flag+"=") {
			return true
		}
	}
	return false
}

// --- named-options recipes: argv built from an options map, not raw extra ---

func boolOption(options map[string]string, key string, def bool) bool {
	v, ok := options[key]
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return b
}

func sortedOptionKeys(options map[string]string) []string {
	keys := make([]string, 0, len(options))
	for k := range options {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// hydraArgs builds `hydra [options] <target> <service>`: username/-l,
// username_file/-L, password/-p, password_file/-P, stop_on_first (-f, default
// true), tasks (-t, default 4), wait (-w), output (-o). The service option is
// required.
func hydraArgs(target string, options map[string]string, extra []string) ([]string, error) {
	service := strings.TrimSpace(options["service"])
	if service == "" {
		return nil, fmt.Errorf(`hydra requires option "service" (ssh, ftp, http-get, http-post-form, smb, ...)`)
	}
	args := []string{"-I"}
	if v := strings.TrimSpace(options["username"]); v != "" {
		args = append(args, "-l", v)
	}
	if v := strings.TrimSpace(options["username_file"]); v != "" {
		args = append(args, "-L", v)
	}
	if v := strings.TrimSpace(options["password"]); v != "" {
		args = append(args, "-p", v)
	}
	if v := strings.TrimSpace(options["password_file"]); v != "" {
		args = append(args, "-P", v)
	}
	if boolOption(options, "stop_on_first", true) {
		args = append(args, "-f")
	}
	tasks := strings.TrimSpace(options["tasks"])
	if tasks == "" {
		tasks = "4"
	}
	args = append(args, "-t", tasks)
	if v := strings.TrimSpace(options["wait"]); v != "" {
		args = append(args, "-w", v)
	}
	if v := strings.TrimSpace(options["output"]); v != "" {
		args = append(args, "-o", v)
	}
	args = append(args, target, service)
	return appendExtra(args, extra), nil
}

// rejectMsfSeparator guards the one recipe whose arguments are re-parsed by
// the target program: msfconsole splits its -x resource string on ';' and line
// breaks, so a value carrying one runs as an extra console command instead of
// a set argument. Every other recipe passes option values as distinct argv
// tokens, where these characters carry no meaning.
func rejectMsfSeparator(label, v string) error {
	if strings.ContainsAny(v, ";\n\r") {
		return fmt.Errorf("metasploit %s %q contains a command separator (';' or a line break)", label, v)
	}
	return nil
}

// metasploitArgs builds `msfconsole -q -x "use <module>; set K V; run; exit"`.
// The scan target is the module path; each option becomes a set command.
func metasploitArgs(target string, options map[string]string, extra []string) ([]string, error) {
	module := strings.TrimSpace(target)
	if !strings.Contains(module, "/") {
		return nil, fmt.Errorf("metasploit target must be a module path (e.g. auxiliary/scanner/smb/smb_version)")
	}
	if err := rejectMsfSeparator("target module", module); err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("use " + module)
	for _, k := range sortedOptionKeys(options) {
		v := strings.TrimSpace(options[k])
		if v == "" {
			continue
		}
		if err := rejectMsfSeparator("option key", k); err != nil {
			return nil, err
		}
		if err := rejectMsfSeparator("option "+k, v); err != nil {
			return nil, err
		}
		b.WriteString("; set " + k + " " + v)
	}
	b.WriteString("; run; exit")
	args := []string{"-q", "-x", b.String()}
	return args, nil
}

var msfvenomReserved = map[string]bool{
	"format": true, "output": true, "encoder": true, "iterations": true,
}

// msfvenomArgs builds `msfvenom -p <payload> [vars...] [flags]`. The scan
// target is the payload; format/output/encoder/iterations map to flags and
// every other option becomes a payload var (LHOST=..., LPORT=...).
func msfvenomArgs(target string, options map[string]string, extra []string) ([]string, error) {
	payload := strings.TrimSpace(target)
	if payload == "" {
		return nil, fmt.Errorf("msfvenom requires a payload as target (e.g. windows/x64/meterpreter/reverse_tcp)")
	}
	args := []string{"-p", payload}
	if v := strings.TrimSpace(options["format"]); v != "" {
		args = append(args, "-f", v)
	}
	if v := strings.TrimSpace(options["output"]); v != "" {
		args = append(args, "-o", v)
	}
	if v := strings.TrimSpace(options["encoder"]); v != "" {
		args = append(args, "-e", v)
	}
	if v := strings.TrimSpace(options["iterations"]); v != "" {
		args = append(args, "-i", v)
	}
	for _, k := range sortedOptionKeys(options) {
		if msfvenomReserved[k] {
			continue
		}
		v := strings.TrimSpace(options[k])
		if v == "" {
			continue
		}
		args = append(args, k+"="+v)
	}
	return appendExtra(args, extra), nil
}

// responderArgs builds `responder -I <iface> [-A] [-w] [-f]`. The scan target
// is the network interface (default eth0). Responder runs until stopped, so the
// run is time-boxed by the scan tool's own timeout instead of a duration flag.
func responderArgs(target string, options map[string]string, extra []string) ([]string, error) {
	iface := strings.TrimSpace(target)
	if iface == "" {
		iface = "eth0"
	}
	args := []string{"-I", iface}
	if boolOption(options, "analyze", false) {
		args = append(args, "-A")
	}
	if boolOption(options, "wpad", false) {
		args = append(args, "-w")
	}
	if boolOption(options, "fingerprint", false) {
		args = append(args, "-f")
	}
	return appendExtra(args, extra), nil
}

// impacketBinary resolves the executable from the script target: .py scripts
// run via python3, packaged impacket-* entrypoints execute directly.
func impacketBinary(target string) string {
	t := strings.TrimSpace(target)
	if strings.HasSuffix(t, ".py") {
		return "python3"
	}
	return t
}

// impacketArgs builds `python3 <script> [tokens...]`. The scan target is the
// script; the "args" option is split on whitespace into argv tokens.
func impacketArgs(target string, options map[string]string, extra []string) ([]string, error) {
	script := strings.TrimSpace(target)
	if script == "" {
		return nil, fmt.Errorf("impacket requires a script as target (e.g. secretsdump.py or impacket-secretsdump)")
	}
	args := []string{script}
	if raw := strings.TrimSpace(options["args"]); raw != "" {
		if strings.ContainsAny(raw, "\n\r") {
			return nil, fmt.Errorf("impacket option %q must not contain line breaks", "args")
		}
		args = append(args, strings.Fields(raw)...)
	}
	return appendExtra(args, extra), nil
}

// netexecArgs builds `netexec <protocol> <target> [-u X] [-p Y] [-H Z] [-M M]`.
func netexecArgs(target string, options map[string]string, extra []string) ([]string, error) {
	proto := strings.TrimSpace(options["protocol"])
	if proto == "" {
		proto = "smb"
	}
	args := []string{proto, target}
	if v := strings.TrimSpace(options["username"]); v != "" {
		args = append(args, "-u", v)
	}
	if v := strings.TrimSpace(options["password"]); v != "" {
		args = append(args, "-p", v)
	}
	if v := strings.TrimSpace(options["hash"]); v != "" {
		args = append(args, "-H", v)
	}
	if v := strings.TrimSpace(options["module"]); v != "" {
		args = append(args, "-M", v)
	}
	return appendExtra(args, extra), nil
}

// fscanArgs builds `fscan -h <target> ...`. The reverse-shell (-rv) and
// file-download (-download) flags are deliberately not exposed: they plant or
// fetch content on the target, which no ledgered recon or verify step needs.
func fscanArgs(target string, options map[string]string, extra []string) ([]string, error) {
	args := []string{"-h", target}
	for _, m := range []struct{ key, flag string }{
		{"ports", "-p"}, {"mode", "-m"}, {"output_file", "-o"},
		{"threads", "-t"}, {"timeout", "-time"}, {"url", "-u"},
		{"cookie", "-cookie"}, {"domain", "-domain"}, {"username", "-user"},
		{"password", "-pwd"}, {"user_file", "-userf"}, {"pass_file", "-pwdf"},
		{"host_file", "-hf"}, {"pocname", "-pocname"}, {"proxy", "-proxy"},
		{"socks5", "-socks5"}, {"iface", "-iface"},
	} {
		if v := strings.TrimSpace(options[m.key]); v != "" {
			args = append(args, m.flag, v)
		}
	}
	return appendExtra(args, extra), nil
}

// bloodhoundArgs builds `bloodhound-python -d <domain> [-u] [-p] [-c] [-dc]`.
func bloodhoundArgs(target string, options map[string]string, extra []string) ([]string, error) {
	domain := strings.TrimSpace(target)
	if domain == "" {
		return nil, fmt.Errorf("bloodhound requires a domain as target")
	}
	args := []string{"-d", domain}
	if v := strings.TrimSpace(options["username"]); v != "" {
		args = append(args, "-u", v)
	}
	if v := strings.TrimSpace(options["password"]); v != "" {
		args = append(args, "-p", v)
	}
	collection := strings.TrimSpace(options["collection"])
	if collection == "" {
		collection = "All"
	}
	args = append(args, "-c", collection)
	if v := strings.TrimSpace(options["dc"]); v != "" {
		args = append(args, "-dc", v)
	}
	return appendExtra(args, extra), nil
}

// linpeasArgs builds `linpeas.sh [-a|-f|-s]`. The scan target is informational
// (local host); mode picks the check intensity.
func linpeasArgs(target string, options map[string]string, extra []string) ([]string, error) {
	var args []string
	switch strings.ToLower(strings.TrimSpace(options["mode"])) {
	case "all":
		args = append(args, "-a")
	case "fast":
		args = append(args, "-f")
	case "superfast":
		args = append(args, "-s")
	}
	return appendExtra(args, extra), nil
}

// hashcatArgs builds `hashcat -m <type> -a <mode> <hashfile> [wordlist|mask]`.
func hashcatArgs(target string, options map[string]string, extra []string) ([]string, error) {
	hashFile := strings.TrimSpace(target)
	if hashFile == "" {
		return nil, fmt.Errorf("hashcat requires a hash file as target")
	}
	hashType := strings.TrimSpace(options["hash_type"])
	if hashType == "" {
		return nil, fmt.Errorf(`hashcat requires option "hash_type" (e.g. 0=MD5, 1000=NTLM, 22000=WPA-PBKDF2)`)
	}
	mode := strings.TrimSpace(options["attack_mode"])
	if mode == "" {
		mode = "0"
	}
	args := []string{"-m", hashType, "-a", mode, hashFile}
	switch {
	case strings.TrimSpace(options["wordlist"]) != "":
		args = append(args, strings.TrimSpace(options["wordlist"]))
	case strings.TrimSpace(options["mask"]) != "":
		args = append(args, strings.TrimSpace(options["mask"]))
	case mode == "0":
		args = append(args, "/usr/share/wordlists/rockyou.txt")
	}
	return appendExtra(args, extra), nil
}

// johnArgs builds `john [--wordlist=W] [--format=F] <hashfile>`.
func johnArgs(target string, options map[string]string, extra []string) ([]string, error) {
	hashFile := strings.TrimSpace(target)
	if hashFile == "" {
		return nil, fmt.Errorf("john requires a hash file as target")
	}
	var args []string
	if v := strings.TrimSpace(options["wordlist"]); v != "" {
		args = append(args, "--wordlist="+v)
	} else {
		args = append(args, "--wordlist=/usr/share/wordlists/rockyou.txt")
	}
	if v := strings.TrimSpace(options["format"]); v != "" {
		args = append(args, "--format="+v)
	}
	args = append(args, hashFile)
	return appendExtra(args, extra), nil
}

// python3Always pins the interpreter for script-runner recipes whose target
// is a file (not the executable).
func python3Always(target string) string { return "python3" }

// pwnRunner wraps a user script so it is passed as argv[1] to a fixed
// prelude — no string interpolation into code, no escaping concerns.
func pwnRunner(prelude string, args []string) []string {
	return append([]string{"-c", prelude}, args...)
}

// dotdotpwnArgs builds `dotdotpwn -m <module> -h <target>`.
func dotdotpwnArgs(target string, options map[string]string, extra []string) ([]string, error) {
	module := strings.TrimSpace(options["module"])
	if module == "" {
		module = "http"
	}
	args := []string{"-m", module, "-h", target}
	return appendExtra(args, extra), nil
}

// hashpumpArgs builds `hashpump -s <sig> -d <data> -k <len> -a <append>`.
// The scan target is the original signed data.
func hashpumpArgs(target string, options map[string]string, extra []string) ([]string, error) {
	for _, key := range []string{"signature", "key_length", "append"} {
		if strings.TrimSpace(options[key]) == "" {
			return nil, fmt.Errorf("hashpump requires option %q", key)
		}
	}
	args := []string{
		"-s", strings.TrimSpace(options["signature"]),
		"-d", target,
		"-k", strings.TrimSpace(options["key_length"]),
		"-a", strings.TrimSpace(options["append"]),
	}
	return args, nil
}

// pacuArgs builds `pacu [--session S] [--modules M] [--regions R]`.
func pacuArgs(target string, options map[string]string, extra []string) ([]string, error) {
	var args []string
	session := strings.TrimSpace(options["session"])
	if session == "" {
		session = "covo_session"
	}
	args = append(args, "--session", session)
	if v := strings.TrimSpace(options["modules"]); v != "" {
		args = append(args, "--modules", v)
	}
	if v := strings.TrimSpace(options["regions"]); v != "" {
		args = append(args, "--regions", v)
	}
	return appendExtra(args, extra), nil
}

const pwntoolsPrelude = `import os,sys
s=sys.argv[1] if len(sys.argv)>1 else ''
b=sys.argv[2] if len(sys.argv)>2 else ''
h=sys.argv[3] if len(sys.argv)>3 else ''
p=sys.argv[4] if len(sys.argv)>4 else ''
t=sys.argv[5] if len(sys.argv)>5 else 'local'
if b: os.environ['PWN_BINARY']=b
if h: os.environ['PWN_HOST']=h
if p: os.environ['PWN_PORT']=p
if t: os.environ['PWN_EXPLOIT_TYPE']=t
exec(s,{})
`

// pwntoolsArgs runs a pwntools script via a fixed prelude; the script and
// PWN_* values travel as argv, never interpolated into code.
func pwntoolsArgs(target string, options map[string]string, extra []string) ([]string, error) {
	script := strings.TrimSpace(options["script"])
	if script == "" {
		return nil, fmt.Errorf(`pwntools requires option "script" (python source using pwntools)`)
	}
	return pwnRunner(pwntoolsPrelude, []string{
		script,
		strings.TrimSpace(options["binary"]),
		strings.TrimSpace(options["host"]),
		strings.TrimSpace(options["port"]),
		strings.TrimSpace(options["exploit_type"]),
	}), nil
}

const angrPrelude = `import sys
s=sys.argv[1] if len(sys.argv)>1 else ''
exec(s,{'binary_path':sys.argv[2] if len(sys.argv)>2 else '','find_address':sys.argv[3] if len(sys.argv)>3 else '','avoid_addresses':sys.argv[4] if len(sys.argv)>4 else '','analysis_type':sys.argv[5] if len(sys.argv)>5 else 'symbolic'})
`

// angrArgs runs an angr script with binary_path/find_address/avoid_addresses
// injected as script globals.
func angrArgs(target string, options map[string]string, extra []string) ([]string, error) {
	script := strings.TrimSpace(options["script"])
	if script == "" {
		return nil, fmt.Errorf(`angr requires option "script" (python source using angr)`)
	}
	binaryPath := strings.TrimSpace(target)
	if binaryPath == "" {
		return nil, fmt.Errorf("angr requires a binary path as target")
	}
	return pwnRunner(angrPrelude, []string{
		script,
		binaryPath,
		strings.TrimSpace(options["find_address"]),
		strings.TrimSpace(options["avoid_addresses"]),
		strings.TrimSpace(options["analysis_type"]),
	}), nil
}

// ghidraArgs builds `analyzeHeadless <dir> <name> -import <binary> [-postScript S]`.
func ghidraArgs(target string, options map[string]string, extra []string) ([]string, error) {
	projectDir := strings.TrimSpace(options["project_dir"])
	if projectDir == "" {
		projectDir = "/tmp/ghidra_projects"
	}
	projectName := strings.TrimSpace(options["project_name"])
	if projectName == "" {
		projectName = "covo_analysis"
	}
	args := []string{projectDir, projectName, "-import", target}
	if v := strings.TrimSpace(options["script"]); v != "" {
		args = append(args, "-postScript", v)
	}
	return appendExtra(args, extra), nil
}
