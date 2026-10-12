package cyber

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type StorageItem struct {
	Path       string    `json:"path"`
	Category   string    `json:"category"`
	Bytes      int64     `json:"bytes"`
	ModifiedAt time.Time `json:"modified_at"`
	Reason     string    `json:"reason"`
}

type StorageReport struct {
	Revision         string        `json:"revision"`
	Items            []StorageItem `json:"items"`
	ReclaimableBytes int64         `json:"reclaimable_bytes"`
	Removed          int           `json:"removed"`
}

func (c *Catalog) CleanStorage(days int, revision string, apply bool) (StorageReport, error) {
	if days < 1 || days > 36500 {
		return StorageReport{}, fmt.Errorf("retention days must be between 1 and 36500")
	}
	catalogMu.Lock()
	defer catalogMu.Unlock()
	report := StorageReport{Items: []StorageItem{}}
	root, err := c.openStorage(false)
	if os.IsNotExist(err) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return report, err
	}
	entries, err := dir.ReadDir(10001)
	dir.Close()
	if err != nil && err != io.EOF {
		return report, err
	}
	if len(entries) > 10000 {
		return report, fmt.Errorf("storage entry budget exceeded")
	}
	sort.Slice(entries, func(first, second int) bool { return entries[first].Name() < entries[second].Name() })
	var references strings.Builder
	ledgerHash := sha256.New()
	readBytes := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := root.Lstat(entry.Name())
		if err != nil || !info.Mode().IsRegular() {
			return report, fmt.Errorf("unreadable evidence ledger %s", entry.Name())
		}
		file, err := root.Open(entry.Name())
		if err != nil {
			return report, err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 8<<20+1))
		file.Close()
		if readErr != nil || len(data) > 8<<20 || !json.Valid(data) {
			return report, fmt.Errorf("invalid or oversized evidence ledger %s", entry.Name())
		}
		readBytes += len(data)
		if readBytes > 32<<20 {
			return report, fmt.Errorf("evidence read budget exceeded")
		}
		ledgerHash.Write(data)
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return report, err
			}
			if value, ok := token.(string); ok {
				references.WriteString(value)
				references.WriteByte(0)
			}
		}
	}
	var runtime EngagementRuntime
	if err := c.readJSON(c.runtimePath(), &runtime); err != nil {
		return report, err
	}
	active := runtime.Status == "active"
	var retests []Retest
	if err := c.readJSON(c.retestsPath(), &retests); err != nil {
		return report, err
	}
	for _, record := range retests {
		active = active || record.Status == "running"
	}
	jobs, err := c.loadIntakeJobsLocked()
	if err != nil {
		return report, err
	}
	for _, job := range jobs {
		active = active || job.Status == "pending" || job.Status == "running"
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	add := func(name, category string) error {
		info, err := root.Lstat(name)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		item := StorageItem{Path: name, Category: category, Bytes: info.Size(), ModifiedAt: info.ModTime(), Reason: "recent"}
		switch {
		case !info.Mode().IsRegular():
			item.Reason = "unsafe"
		case active:
			item.Reason = "active"
		case strings.Contains(references.String(), filepath.Base(name)):
			item.Reason = "referenced"
		case info.ModTime().Before(cutoff):
			item.Reason = "expired"
			report.ReclaimableBytes += info.Size()
		}
		report.Items = append(report.Items, item)
		return nil
	}
	if info, err := root.Lstat("outputs"); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return report, fmt.Errorf("unsafe output directory")
		}
		outputs, err := root.Open("outputs")
		if err != nil {
			return report, err
		}
		files, err := outputs.ReadDir(10001)
		outputs.Close()
		if err != nil && err != io.EOF {
			return report, err
		}
		if len(files) > 10000 {
			return report, fmt.Errorf("output entry budget exceeded")
		}
		for _, file := range files {
			if err := add(filepath.Join("outputs", file.Name()), "outputs"); err != nil {
				return report, err
			}
		}
	} else if !os.IsNotExist(err) {
		return report, err
	}
	for _, name := range []string{"scan_report.md", "findings.csv", "findings.sarif"} {
		if err := add(name, "reports"); err != nil {
			return report, err
		}
	}
	sort.Slice(report.Items, func(first, second int) bool { return report.Items[first].Path < report.Items[second].Path })
	data, _ := json.Marshal(report.Items)
	report.Revision = fmt.Sprintf("%x", sha256.Sum256(append(data, ledgerHash.Sum(nil)...)))
	if !apply {
		return report, nil
	}
	if revision == "" || revision != report.Revision {
		return report, fmt.Errorf("cleanup preview changed; inspect storage again")
	}
	for _, item := range report.Items {
		if item.Reason != "expired" {
			continue
		}
		info, err := root.Lstat(item.Path)
		if err != nil || !info.Mode().IsRegular() || info.Size() != item.Bytes || !info.ModTime().Equal(item.ModifiedAt) {
			return report, fmt.Errorf("cleanup candidate changed")
		}
		if err := root.Remove(item.Path); err != nil {
			return report, err
		}
		report.Removed++
	}
	return report, nil
}
