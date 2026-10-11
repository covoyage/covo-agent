package evolution

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path"
	"sync"

	covoagent "github.com/covoyage/covo-agent"
)

var bundledSkillHashes struct {
	sync.Once
	values map[string]bool
}

func skillPackageHash(files fs.FS) (string, error) {
	hash := sha256.New()
	var totalBytes int64
	var entries int
	err := fs.WalkDir(files, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		entries++
		if entries > maxFileCount*4 {
			return fs.ErrInvalid
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fs.ErrInvalid
		}
		file, err := files.Open(name)
		if err != nil {
			return err
		}
		defer file.Close()
		io.WriteString(hash, name+"\x00")
		copied, err := io.Copy(hash, io.LimitReader(file, maxTotalSizeKB*1024-totalBytes+1))
		if err != nil {
			return err
		}
		totalBytes += copied
		if totalBytes > maxTotalSizeKB*1024 {
			return fs.ErrInvalid
		}
		io.WriteString(hash, "\x00")
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func MatchesBundledSkill(directory string) bool {
	bundledSkillHashes.Do(func() {
		bundledSkillHashes.values = make(map[string]bool)
		fs.WalkDir(covoagent.BundledSkillsFS, "skills", func(name string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || entry.Name() != "SKILL.md" {
				return err
			}
			files, err := fs.Sub(covoagent.BundledSkillsFS, path.Dir(name))
			if err != nil {
				return err
			}
			digest, err := skillPackageHash(files)
			if err == nil {
				bundledSkillHashes.values[digest] = true
			}
			return nil
		})
	})
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() {
		return false
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return false
	}
	defer root.Close()
	digest, err := skillPackageHash(root.FS())
	return err == nil && bundledSkillHashes.values[digest]
}
