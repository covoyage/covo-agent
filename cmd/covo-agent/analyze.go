package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

func newAnalyzeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "analyze [path]",
		Short: "Analyze a repository",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) > 0 {
				path = args[0]
			}
			absPath, err := filepath.Abs(path)
			if err != nil {
				return fmt.Errorf("error resolving path: %w", err)
			}
			info, err := os.Stat(absPath)
			if err != nil {
				return fmt.Errorf("error accessing path: %w", err)
			}
			if !info.IsDir() {
				return fmt.Errorf("path is not a directory")
			}
			w := cmd.OutOrStdout()
			fmt.Fprintln(w, "  ── Workspace Analysis ──")
			fmt.Fprintf(w, "  Path: %s\n\n", absPath)
			projectType, projectInfo := detectProject(absPath)
			fmt.Fprintf(w, "  Project: %s (%s)\n", filepath.Base(absPath), projectType)
			for _, line := range projectInfo {
				fmt.Fprintln(w, line)
			}
			fmt.Fprintln(w)
			gitInfo := getGitInfo(absPath)
			for _, line := range gitInfo {
				fmt.Fprintln(w, line)
			}
			if len(gitInfo) > 0 {
				fmt.Fprintln(w)
			}
			extCounts, totalFiles, totalLines := countFiles(absPath)
			fmt.Fprintf(w, "  Files: %d total\n", totalFiles)
			exts := make([]string, 0, len(extCounts))
			for ext := range extCounts {
				exts = append(exts, ext)
			}
			sort.Slice(exts, func(i, j int) bool {
				if exts[i] == "other" {
					return false
				}
				if exts[j] == "other" {
					return true
				}
				return extCounts[exts[i]] > extCounts[exts[j]]
			})
			for _, ext := range exts {
				fmt.Fprintf(w, "    %-10s %d files\n", ext, extCounts[ext])
			}
			fmt.Fprintf(w, "    Lines of code: ~%d\n\n", totalLines)
			fmt.Fprintln(w, "  Top-level Structure:")
			printTree(absPath, absPath, 0, 3)
			return nil
		},
	}
}

func detectProject(root string) (string, []string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "unknown", nil
	}
	entrySet := make(map[string]bool, len(entries))
	for _, e := range entries {
		entrySet[e.Name()] = true
	}
	if entrySet["go.mod"] {
		return "Go", readGoMod(root)
	}
	if entrySet["package.json"] {
		return "Node", readPackageJSON(root)
	}
	if _, ok := entrySet["Cargo.toml"]; ok {
		return "Rust", readCargoToml(root)
	}
	if entrySet["Gemfile"] {
		return "Ruby", nil
	}
	if entrySet["pyproject.toml"] || entrySet["setup.py"] || entrySet["requirements.txt"] {
		var info []string
		if entrySet["pyproject.toml"] {
			info = append(info, "  pyproject.toml detected")
		}
		if entrySet["setup.py"] {
			info = append(info, "  setup.py detected")
		}
		if entrySet["requirements.txt"] {
			info = append(info, "  requirements.txt detected")
		}
		return "Python", info
	}
	var generic []string
	if entrySet["Makefile"] {
		generic = append(generic, "  Makefile")
	}
	if entrySet["Dockerfile"] {
		generic = append(generic, "  Dockerfile")
	}
	if entrySet[".github"] {
		generic = append(generic, "  CI: GitHub Actions")
	}
	if entrySet[".gitlab-ci.yml"] {
		generic = append(generic, "  CI: GitLab CI")
	}
	if entrySet["Jenkinsfile"] {
		generic = append(generic, "  CI: Jenkins")
	}
	return "Generic", generic
}

func readGoMod(root string) []string {
	f, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var info []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			info = append(info, "  Module: "+strings.TrimPrefix(line, "module "))
		} else if strings.HasPrefix(line, "go ") && !strings.Contains(line, "(") {
			info = append(info, "  Go Version: "+strings.TrimPrefix(line, "go "))
		}
	}
	return info
}

func readPackageJSON(root string) []string {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	content := string(data)
	var info []string
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "\"name\"") {
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				name := strings.Trim(strings.TrimSpace(parts[1]), "\",")
				info = append(info, "  Name: "+name)
			}
		}
		if strings.HasPrefix(trimmed, "\"dependencies\"") {
			deps := countPackageJSONDeps(lines)
			if deps > 0 {
				info = append(info, fmt.Sprintf("  Dependencies: %d", deps))
			}
		}
	}
	return info
}

func countPackageJSONDeps(lines []string) int {
	count := 0
	inDeps := false
	depth := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "\"dependencies\"") {
			inDeps = true
			if strings.Contains(trimmed, "{") {
				depth++
			}
			continue
		}
		if inDeps {
			if strings.Contains(trimmed, "{") {
				depth++
			}
			if strings.Contains(trimmed, "}") {
				depth--
				if depth == 0 {
					break
				}
			}
			if depth == 1 && strings.Contains(trimmed, ":") {
				count++
			}
		}
	}
	return count
}

func readCargoToml(root string) []string {
	f, err := os.Open(filepath.Join(root, "Cargo.toml"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var info []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "name") && strings.Contains(line, "=") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				info = append(info, "  Crate: "+strings.Trim(strings.TrimSpace(parts[1]), "\" "))
			}
		}
		if strings.HasPrefix(line, "edition") && strings.Contains(line, "=") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				info = append(info, "  Edition: "+strings.TrimSpace(parts[1]))
			}
		}
		if len(info) >= 2 {
			break
		}
	}
	return info
}

func getGitInfo(root string) []string {
	if _, err := exec.LookPath("git"); err != nil {
		return nil
	}
	cmd := exec.Command("git", "-C", root, "rev-parse", "--is-inside-work-tree")
	if err := cmd.Run(); err != nil {
		return nil
	}
	var info []string
	branchCmd := exec.Command("git", "-C", root, "rev-parse", "--abbrev-ref", "HEAD")
	branchOut, err := branchCmd.Output()
	if err == nil {
		branch := strings.TrimSpace(string(branchOut))
		countCmd := exec.Command("git", "-C", root, "rev-list", "--count", "HEAD")
		countOut, err := countCmd.Output()
		if err == nil {
			info = append(info, fmt.Sprintf("  Git: %s (%s commits)", branch, strings.TrimSpace(string(countOut))))
		} else {
			info = append(info, fmt.Sprintf("  Git: %s", branch))
		}
		remoteCmd := exec.Command("git", "-C", root, "remote", "get-url", "origin")
		if remoteOut, err := remoteCmd.Output(); err == nil {
			info = append(info, "  Remote: "+strings.TrimSpace(string(remoteOut)))
		}
		logCmd := exec.Command("git", "-C", root, "log", "-1", "--format=%H")
		hashOut, err := logCmd.Output()
		if err == nil {
			hash := strings.TrimSpace(string(hashOut))
			shortHash := hash
			if len(hash) > 7 {
				shortHash = hash[:7]
			}
			msgCmd := exec.Command("git", "-C", root, "log", "-1", "--format=%s")
			msgOut, _ := msgCmd.Output()
			msg := strings.TrimSpace(string(msgOut))
			authorCmd := exec.Command("git", "-C", root, "log", "-1", "--format=%an")
			authorOut, _ := authorCmd.Output()
			author := strings.TrimSpace(string(authorOut))
			dateCmd := exec.Command("git", "-C", root, "log", "-1", "--format=%as")
			dateOut, _ := dateCmd.Output()
			date := strings.TrimSpace(string(dateOut))
			info = append(info, fmt.Sprintf("  Last: \"%s\" by %s (%s) [%s]", msg, author, date, shortHash))
		}
	}
	return info
}

var skipDirs = map[string]bool{
	"vendor":      true,
	"node_modules": true,
	".git":        true,
	".next":       true,
	"dist":        true,
	"build":       true,
	"target":      true,
}

func countFiles(root string) (map[string]int, int, int) {
	extCounts := make(map[string]int)
	totalFiles := 0
	totalLines := 0
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return filepath.SkipDir
		}
		if info.IsDir() && path != root {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.IsDir() {
			totalFiles++
			ext := filepath.Ext(info.Name())
			if ext == "" {
				ext = "(no ext)"
			}
			extCounts[ext]++
			fi, err := os.Stat(path)
			if err == nil && fi.Size() < 1<<20 {
				f, err := os.Open(path)
				if err == nil {
					scanner := bufio.NewScanner(f)
					lines := 0
					for scanner.Scan() {
						lines++
					}
					f.Close()
					totalLines += lines
				}
			}
		}
		return nil
	})
	grouped := make(map[string]int)
	var otherCount int
	for ext, count := range extCounts {
		switch ext {
		case ".go", ".ts", ".js", ".py", ".md", ".json", ".yaml", ".yml", ".toml", ".css", ".html", ".rs", ".rb", ".java", ".kt", ".swift":
			grouped[ext] += count
		default:
			otherCount += count
		}
	}
	if otherCount > 0 {
		grouped["other"] = otherCount
	}
	return grouped, totalFiles, totalLines
}

func printTree(root, current string, depth, maxDepth int) {
	printTreeAt(root, current, "", depth, maxDepth)
}

func printTreeAt(root, current, prefix string, depth, maxDepth int) {
	if depth >= maxDepth {
		return
	}
	entries, err := os.ReadDir(current)
	if err != nil {
		return
	}
	var dirs []os.DirEntry
	var files []os.DirEntry
	for _, e := range entries {
		if skipDirs[e.Name()] {
			continue
		}
		if strings.HasPrefix(e.Name(), ".") && depth > 0 {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name() < dirs[j].Name() })
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })
	allEntries := append(dirs, files...)
	for i, entry := range allEntries {
		isLast := i == len(allEntries)-1
		branch := "├── "
		if isLast {
			branch = "└── "
		}
		if entry.IsDir() {
			fileCount := countDirFiles(filepath.Join(current, entry.Name()))
			fmt.Printf("%s%s%s/ (%d files)\n", prefix, branch, entry.Name(), fileCount)
			childPrefix := prefix + "│  "
			if isLast {
				childPrefix = prefix + "   "
			}
			printTreeAt(root, filepath.Join(current, entry.Name()), childPrefix, depth+1, maxDepth)
		} else {
			fmt.Printf("%s%s%s\n", prefix, branch, entry.Name())
		}
	}
}

func countDirFiles(dir string) int {
	count := 0
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return filepath.SkipDir
		}
		if info.IsDir() && path != dir {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
		}
		if !info.IsDir() {
			count++
		}
		return nil
	})
	return count
}
