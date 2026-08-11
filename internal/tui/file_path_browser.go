package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/covoyage/covonaut/tui/core"
)

// FilePathBrowser provides interactive file or folder autocompletion.
type FilePathBrowser struct {
	trigger      string
	rootDir      string
	fuzzyEnabled func() bool
}

func NewFilePathBrowser(trigger, rootDir string, fuzzyEnabled func() bool) *FilePathBrowser {
	return &FilePathBrowser{trigger: trigger, rootDir: rootDir, fuzzyEnabled: fuzzyEnabled}
}

func (browser *FilePathBrowser) Trigger() string { return browser.trigger }

func (browser *FilePathBrowser) Complete(token string) []core.Suggestion {
	root := browser.rootDir
	if root == "" {
		workingDirectory, err := os.Getwd()
		if err != nil {
			return nil
		}
		root = workingDirectory
	}

	mode := "file"
	if browser.trigger == "@folder" {
		mode = "folder"
	}

	skipped := ""
	cleanToken := token
	if strings.HasPrefix(token, ":") || strings.HasPrefix(token, " ") {
		skipped = string(token[0])
		cleanToken = token[1:]
	}
	directory, prefix := filepath.Split(cleanToken)
	searchPath := filepath.Join(root, directory)
	if !strings.HasSuffix(directory, string(os.PathSeparator)) && directory == "" {
		searchPath = root
	}

	entries, err := os.ReadDir(searchPath)
	if err != nil {
		if len(cleanToken) == 0 {
			return nil
		}
		displaySuffix := "/"
		if strings.HasSuffix(cleanToken, "/") {
			displaySuffix = ""
		}
		return []core.Suggestion{{
			InsertText:  skipped + cleanToken + displaySuffix,
			Label:       skipped + cleanToken + displaySuffix,
			Description: "create/open",
		}}
	}

	type scoredSuggestion struct {
		suggestion core.Suggestion
		score      int64
	}
	var scored []scoredSuggestion
	useFuzzy := browser.fuzzyEnabled != nil && browser.fuzzyEnabled()

	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}

		matchScore := int64(0)
		if useFuzzy {
			match := core.FuzzyMatchOne(prefix, name)
			if match.Score < 0 {
				continue
			}
			matchScore = match.Score
		} else if !strings.Contains(strings.ToLower(name), strings.ToLower(prefix)) {
			continue
		}

		isDirectory := entry.IsDir()
		if mode == "folder" && !isDirectory {
			continue
		}

		fullPath := directory + name
		suggestion := core.Suggestion{InsertText: skipped + fullPath}
		if isDirectory {
			suggestion.Label = "📁 " + name + "/"
			suggestion.InsertText += "/"
			suggestion.Description = "directory"
		} else {
			suggestion.Label = "📄 " + name
			suggestion.Description = formatFileSize(entry)
		}
		scored = append(scored, scoredSuggestion{suggestion: suggestion, score: matchScore})
	}

	sort.Slice(scored, func(left, right int) bool {
		if useFuzzy && scored[left].score != scored[right].score {
			return scored[left].score > scored[right].score
		}
		return scored[left].suggestion.Label < scored[right].suggestion.Label
	})

	suggestions := make([]core.Suggestion, len(scored))
	for index, item := range scored {
		suggestions[index] = item.suggestion
	}
	return suggestions
}

func formatFileSize(entry os.DirEntry) string {
	info, err := entry.Info()
	if err != nil {
		return ""
	}
	size := info.Size()
	switch {
	case size < 1024:
		return ""
	case size < 1024*1024:
		kilobytes := size / 1024
		if kilobytes < 10 {
			return ""
		}
		return fmt.Sprintf("%d KB", kilobytes)
	default:
		megabytes := size / (1024 * 1024)
		if megabytes < 1 {
			return ""
		}
		return fmt.Sprintf("%d MB", megabytes)
	}
}

var _ core.AutocompleteProvider = (*FilePathBrowser)(nil)
