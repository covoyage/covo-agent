package agent

import "strings"

// gitReadOnlySubcommands are git verbs that do not mutate the repository or
// index. Unknown subcommands are treated as mutating.
var gitReadOnlySubcommands = map[string]bool{
	"status": true, "log": true, "show": true, "diff": true,
	"ls-files": true, "ls-remote": true, "rev-parse": true, "describe": true,
	"blame": true, "cat-file": true, "shortlog": true, "version": true,
	"help": true, "grep": true, "name-rev": true, "rev-list": true,
	"var": true, "check-ignore": true, "check-attr": true, "whatchanged": true,
	"range-diff": true, "format-patch": true,
}

// gitFlagsWithArg are git global flags that consume the following token.
var gitFlagsWithArg = map[string]bool{
	"-C": true, "-c": true, "--git-dir": true, "--work-tree": true,
	"--namespace": true, "--config-env": true, "-o": true,
}

// ghReadOnly is "command/subcommand" for the GitHub CLI. The empty
// subcommand value means any subcommand of that command is read-only.
var ghReadOnly = map[string]map[string]bool{
	"pr": {
		"view": true, "list": true, "status": true, "diff": true,
		"checks": true, "files": true, "comment": false,
	},
	"issue": {
		"view": true, "list": true, "status": true,
	},
	"repo": {
		"view": true, "list": true,
	},
	"api":     {},
	"search":  {"issues": true, "prs": true, "repos": true, "code": true},
	"browse":  {},
	"status":  {},
	"auth":    {"status": true},
	"gist":    {"list": true, "view": true},
	"release": {"list": true, "view": true, "download": true},
	"run":     {"list": true, "view": true, "watch": true},
	"cache":   {"list": true},
}

// ClassifyArgv reports whether a tokenized command is read-only.
// cmd is the resolved binary name (no path); args are the remaining tokens,
// excluding shell operators.
func ClassifyArgv(cmd string, args []string) (readOnly bool) {
	switch cmd {
	case "git":
		return classifyGit(args)
	case "gh":
		return classifyGH(args)
	default:
		return readOnlyCommands[cmd]
	}
}

func classifyGit(args []string) bool {
	i := 0
	for i < len(args) {
		tok := args[i]
		if tok == "--" {
			i++
			break
		}
		if strings.HasPrefix(tok, "-") {
			key, inlineVal := splitFlag(tok)
			if gitFlagsWithArg[key] && inlineVal == "" {
				i += 2
				continue
			}
			i++
			continue
		}
		break
	}
	if i >= len(args) {
		return true
	}
	sub := args[i]
	if sub == "stash" {
		if i+1 < len(args) {
			switch args[i+1] {
			case "list", "show":
				return true
			}
		}
		return false
	}
	if sub == "branch" || sub == "tag" || sub == "remote" || sub == "config" {
		return gitReadOnlyWithFlags(sub, args[i+1:])
	}
	return gitReadOnlySubcommands[sub]
}

func gitReadOnlyWithFlags(sub string, rest []string) bool {
	for _, tok := range rest {
		if !strings.HasPrefix(tok, "-") {
			continue
		}
		switch sub {
		case "branch":
			if hasAnyFlag(tok, "d", "D", "m", "M", "c", "C") || tok == "--delete" || tok == "--move" || tok == "--copy" {
				return false
			}
		case "tag":
			if hasAnyFlag(tok, "d", "a", "s", "u", "f") || tok == "--delete" || tok == "--annotate" || tok == "--sign" || tok == "--force" {
				return false
			}
		case "remote":
			switch strings.TrimLeft(tok, "-") {
			case "v", "verbose":
				continue
			}
			return false
		case "config":
			if tok == "--get" || tok == "--get-all" || tok == "--get-regexp" || tok == "--list" || tok == "-l" {
				return true
			}
			if hasAnyFlag(tok, "e") || tok == "--edit" || tok == "--add" || tok == "--unset" || tok == "--unset-all" || tok == "--replace-all" {
				return false
			}
		}
	}
	if sub == "config" {
		for _, tok := range rest {
			if !strings.HasPrefix(tok, "-") {
				return false
			}
		}
		return true
	}
	if sub == "remote" {
		for _, tok := range rest {
			if !strings.HasPrefix(tok, "-") {
				switch tok {
				case "show", "get-url":
					return true
				default:
					return false
				}
			}
		}
		return true
	}
	return true
}

func classifyGH(args []string) bool {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		i++
	}
	if i >= len(args) {
		return true
	}
	cmd := args[i]
	subs, ok := ghReadOnly[cmd]
	if !ok {
		return false
	}
	if len(subs) == 0 {
		return true
	}
	i++
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		i++
	}
	if i >= len(args) {
		return subs[""]
	}
	return subs[args[i]]
}

func splitFlag(tok string) (key, value string) {
	if strings.HasPrefix(tok, "--") {
		if i := strings.IndexByte(tok, '='); i >= 0 {
			return tok[:i], tok[i+1:]
		}
		return tok, ""
	}
	if len(tok) > 2 && !strings.HasPrefix(tok, "--") {
		return tok[:2], tok[2:]
	}
	return tok, ""
}

func hasAnyFlag(tok string, shorts ...string) bool {
	if strings.HasPrefix(tok, "--") {
		return false
	}
	body := strings.TrimPrefix(tok, "-")
	for _, s := range shorts {
		if strings.Contains(body, s) {
			return true
		}
	}
	return false
}

// commandTokens returns the leading simple-command tokens of line, stopping
// at the first shell operator. Assignments at the front are skipped.
func commandTokens(line string) (cmd string, args []string) {
	tokens := tokenizeShell(line)
	if len(tokens) == 0 {
		return "", nil
	}
	i := 0
	for i < len(tokens) && isAssignment([]string{tokens[i]}) {
		i++
	}
	if i >= len(tokens) {
		return "", nil
	}
	cmd = tokens[i]
	cmd = stripShellBuiltins(cmd)
	cmd = strings.TrimPrefix(cmd, `\`)
	cmd = stripPath(cmd)
	rest := tokens[i+1:]
	var argsOut []string
	for _, t := range rest {
		if t == "|" || t == ";" || t == "&" || t == ">" || t == "<" {
			break
		}
		if t == "&&" || t == "||" {
			break
		}
		argsOut = append(argsOut, t)
	}
	return cmd, argsOut
}
