package cyber

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type ScanRequest struct {
	Recipe Recipe
	Target string
	// Options are named recipe options, e.g.
	// {"service": "ssh", "username_file": "users.txt"} for hydra.
	Options map[string]string
	Extra   []string
}

type ScanPlan struct {
	Command     []string
	Binary      string
	Timeout     time.Duration
	AllowedExit []int
	Summary     string
}

func PlanScan(req ScanRequest) (ScanPlan, error) {
	recipe := Recipe(strings.ToLower(strings.TrimSpace(string(req.Recipe))))
	target := strings.TrimSpace(req.Target)
	if recipe == RecipeList {
		return ScanPlan{}, nil
	}
	spec, ok := LookupRecipe(recipe)
	if !ok {
		return ScanPlan{}, fmt.Errorf("unknown scan recipe %q — use recipe \"list\"", req.Recipe)
	}
	if target == "" {
		return ScanPlan{}, fmt.Errorf("scan target is required")
	}
	args, err := buildRecipeArgs(spec, target, req.Options, req.Extra)
	if err != nil {
		return ScanPlan{}, err
	}
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	bin := spec.Binary
	if spec.BinaryFor != nil {
		bin = spec.BinaryFor(target)
	}
	return ScanPlan{
		Binary:      bin,
		Command:     append([]string{bin}, args...),
		Timeout:     timeout,
		AllowedExit: spec.AllowedExit,
		Summary:     spec.Summary,
	}, nil
}

func (c *Catalog) CheckScanRequestScope(req ScanRequest) error {
	spec, ok := LookupRecipe(req.Recipe)
	if !ok {
		return nil
	}
	if err := c.CheckActiveTarget(req.Target, spec.TargetKind); err != nil {
		return err
	}
	if len(req.Extra) > 0 && (spec.TargetKind == TargetHost || spec.TargetKind == TargetURL || spec.TargetKind == TargetModule) {
		return fmt.Errorf("extra flags are not supported for scoped network recipes")
	}
	switch spec.Name {
	case "metasploit":
		hasHost := false
		for key, raw := range req.Options {
			value := strings.TrimSpace(raw)
			switch strings.ToUpper(strings.TrimSpace(key)) {
			case "RHOSTS", "RHOST":
				if value == "" || strings.ContainsAny(value, ", \t\n") {
					return fmt.Errorf("metasploit requires one explicit host per RHOSTS/RHOST field")
				}
				if err := c.CheckActiveTarget(value, TargetHost); err != nil {
					return err
				}
				hasHost = true
			case "RPORT", "THREADS", "SSL", "TARGETURI", "USERNAME", "PASSWORD", "SMBUSER", "SMBPASS", "SMBDOMAIN", "VERBOSE":
				if strings.EqualFold(key, "TARGETURI") && strings.Contains(value, "://") {
					if err := c.CheckActiveTarget(value, TargetURL); err != nil {
						return err
					}
				}
			default:
				return fmt.Errorf("metasploit option %q has no scoped target policy", key)
			}
		}
		if !hasHost {
			return fmt.Errorf("metasploit requires one explicit RHOSTS target")
		}
	case "impacket":
		return fmt.Errorf("impacket args cannot be scoped reliably; use a separately approved isolated runner")
	case "fscan":
		for _, option := range []string{"host_file", "proxy", "socks5", "iface"} {
			if strings.TrimSpace(req.Options[option]) != "" {
				return fmt.Errorf("fscan option %s may reach additional targets", option)
			}
		}
		if target := strings.TrimSpace(req.Options["url"]); target != "" {
			if err := c.CheckActiveTarget(target, TargetURL); err != nil {
				return err
			}
		}
		if domain := strings.TrimSpace(req.Options["domain"]); domain != "" {
			if err := c.CheckActiveTarget(domain, TargetHost); err != nil {
				return err
			}
		}
	case "bloodhound":
		if dc := strings.TrimSpace(req.Options["dc"]); dc != "" {
			return c.CheckActiveTarget(dc, TargetHost)
		}
	}
	return nil
}

func LookPath(binary string) (string, error) {
	path, err := exec.LookPath(binary)
	if err != nil {
		return "", fmt.Errorf("%s is not installed or not on PATH (see covo-agent doctor)", binary)
	}
	return path, nil
}

// buildRecipeArgs dispatches between the named-options builders (new-style
// recipes) and the positional Args closures (original catalog recipes).
func buildRecipeArgs(spec RecipeSpec, target string, options map[string]string, extra []string) ([]string, error) {
	if spec.OptionsArgs != nil {
		opts := options
		if opts == nil {
			opts = map[string]string{}
		}
		return spec.OptionsArgs(target, opts, extra)
	}
	if spec.Args == nil {
		return nil, fmt.Errorf("recipe %q has no argument builder", spec.Name)
	}
	return spec.Args(target, extra)
}

// appendExtra appends operator-provided extra flags verbatim. Execution is
// direct argv (no shell), so metacharacters are passed through untouched.
func appendExtra(args, extra []string) []string {
	for _, e := range extra {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		args = append(args, e)
	}
	return args
}
