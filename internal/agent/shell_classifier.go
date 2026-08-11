package agent

import (
	"strings"
)

var readOnlyCommands = map[string]bool{
	"ls":        true,
	"cat":       true,
	"head":      true,
	"tail":      true,
	"less":      true,
	"more":      true,
	"grep":      true,
	"rg":        true,
	"find":      true,
	"locate":    true,
	"which":     true,
	"whereis":   true,
	"type":      true,
	"command":   true,
	"echo":      true,
	"printf":    true,
	"pwd":       true,
	"env":       true,
	"printenv":  true,
	"uname":     true,
	"hostname":  true,
	"whoami":    true,
	"id":        true,
	"groups":    true,
	"date":      true,
	"uptime":    true,
	"df":        true,
	"du":        true,
	"free":      true,
	"top":       true,
	"ps":        true,
	"pgrep":     true,
	"pidof":     true,
	"lsof":      true,
	"fuser":     true,
	"netstat":   true,
	"ss":        true,
	"ip":        true,
	"ifconfig":  true,
	"ping":      true,
	"dig":       true,
	"nslookup":  true,
	"host":      true,
	"curl":      true,
	"wget":      true,
	"wc":        true,
	"sort":      true,
	"uniq":      true,
	"cut":       true,
	"tr":        true,
	"awk":       true,
	"sed":       true,
	"jq":        true,
	"yq":        true,
	"column":    true,
	"diff":      true,
	"cmp":       true,
	"comm":      true,
	"file":      true,
	"stat":      true,
	"md5":       true,
	"md5sum":    true,
	"sha1sum":   true,
	"sha256sum": true,
	"sha512sum": true,
	"base64":    true,
	"xxd":       true,
	"hexdump":   true,
	"od":        true,
	"strings":   true,
	"readlink":  true,
	"realpath":  true,
	"dirname":   true,
	"basename":  true,
	"tree":      true,
	"git":       true,
	"hg":        true,
	"svn":       true,
	"node":      true,
	"python":    true,
	"python3":   true,
	"ruby":      true,
	"perl":      true,
	"go":        true,
	"rustc":     true,
	"cargo":     true,
	"npm":       true,
	"npx":       true,
	"yarn":      true,
	"pnpm":      true,
	"make":      true,
	"cmake":     true,
	"gcc":       true,
	"g++":       true,
	"clang":     true,
	"clang++":   true,
	"cc":        true,
	"as":        true,
	"ld":        true,
	"ar":        true,
	"nm":        true,
	"objdump":   true,
	"strip":     true,
	"nano":      true,
	"vim":       true,
	"vi":        true,
	"emacs":     true,
	"code":      true,
	"open":      true,
	"xdg-open":  true,
	"man":       true,
	"info":      true,
	"whatis":    true,
	"apropos":   true,
	"clear":     true,
	"reset":     true,
	"true":      true,
	"false":     true,
	"test":      true,
	"expr":      true,
	"sleep":     true,
	"wait":      true,
	"timeout":   true,
	"xargs":     true,
	"tee":       true,
	"tar":       true,
	"gzip":      true,
	"gunzip":    true,
	"bzip2":     true,
	"bunzip2":   true,
	"xz":        true,
	"unxz":      true,
	"zipinfo":   true,
	"unzip":     true,
	"brew":      true,
	"apt":       true,
	"apt-get":   true,
	"dpkg":      true,
	"rpm":       true,
	"yum":       true,
	"dnf":       true,
	"pacman":    true,
	"snap":      true,
	"flatpak":   true,
	"pip":       true,
	"pip3":      true,
	"gem":       true,
	"cpan":      true,
	"luarocks":  true,
	"docker":    true,
	"kubectl":   true,
	"helm":      true,
	"terraform": true,
	"ansible":   true,
	"ssh":       true,
	"scp":       true,
	"rsync":     true,
	"nc":        true,
	"telnet":    true,
	"nmap":      true,
	"tcpdump":   true,
	"strace":    true,
	"ltrace":    true,
	"gdb":       true,
	"lldb":      true,
	"perf":      true,
	"valgrind":  true,
	"time":      true,
	"nice":      true,
	"nohup":     true,
	"watch":     true,
	"history":   true,
	"alias":     true,
	"unalias":   true,
	"declare":   true,
	"typeset":   true,
	"local":     true,
	"export":    true,
	"readonly":  true,
	"shift":     true,
	"getopts":   true,
	"hash":      true,
	"ulimit":    true,
	"umask":     true,
	"cd":        true,
	"pushd":     true,
	"popd":      true,
	"dirs":      true,
	"source":    true,
	"fg":        true,
	"bg":        true,
	"jobs":      true,
	"disown":    true,
	"kill":      true,
	"trap":      true,
	"set":       true,
	"shopt":     true,
	"bind":      true,
	"complete":  true,
	"compgen":   true,
	"compopt":   true,
	"mapfile":   true,
	"readarray": true,
	"read":      true,
	"help":      true,
	"builtin":   true,
	"caller":    true,
	"coproc":    true,
	"eval":      true,
	"exec":      true,
	"exit":      true,
	"logout":    true,
	"return":    true,
	"break":     true,
	"continue":  true,
	"let":       true,
	"select":    true,
	"until":     true,
	"while":     true,
	"for":       true,
	"case":      true,
	"if":        true,
	"then":      true,
	"else":      true,
	"elif":      true,
	"fi":        true,
	"done":      true,
	"esac":      true,
	"do":        true,
	"in":        true,
	"function":  true,
}

var readOnlyCompoundCommands = map[string]bool{
	"[[": true,
	"(":  true,
	"{":  true,
}

func ClassifyShellCommand(command string) (isReadOnly bool, reason string) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return true, ""
	}

	lines := strings.Split(trimmed, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !isReadOnlyLine(line) {
			return false, ""
		}
	}
	return true, ""
}

func isReadOnlyLine(line string) bool {
	tokens := tokenizeShell(line)
	if len(tokens) == 0 {
		return true
	}

	if isAssignment(tokens) {
		return true
	}

	cmd := tokens[0]
	cmd = stripShellBuiltins(cmd)
	cmd = strings.TrimPrefix(cmd, `\`)
	cmd = stripPath(cmd)

	if readOnlyCommands[cmd] {
		return true
	}

	if readOnlyCompoundCommands[cmd] {
		return true
	}

	return false
}

func stripShellBuiltins(cmd string) string {
	builtins := map[string]bool{
		"command": true, "builtin": true, "exec": true,
		"nohup": true, "nice": true, "time": true,
		"timeout": true, "env": true, "sudo": true,
	}
	for {
		if !builtins[cmd] {
			break
		}
		cmd = ""
	}
	return cmd
}

func stripPath(cmd string) string {
	if idx := strings.LastIndex(cmd, "/"); idx >= 0 {
		return cmd[idx+1:]
	}
	return cmd
}

func isAssignment(tokens []string) bool {
	return strings.Contains(tokens[0], "=") && !strings.HasPrefix(tokens[0], "-")
}

func tokenizeShell(line string) []string {
	var tokens []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false

	for _, ch := range line {
		if escaped {
			current.WriteRune(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if inSingle {
			if ch == '\'' {
				inSingle = false
			}
			current.WriteRune(ch)
			continue
		}
		if inDouble {
			if ch == '"' {
				inDouble = false
			}
			current.WriteRune(ch)
			continue
		}
		if ch == '\'' {
			inSingle = true
			continue
		}
		if ch == '"' {
			inDouble = true
			continue
		}
		if ch == ' ' || ch == '\t' {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			continue
		}
		if ch == '|' || ch == ';' || ch == '&' || ch == '>' || ch == '<' {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			tokens = append(tokens, string(ch))
			continue
		}
		current.WriteRune(ch)
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}
