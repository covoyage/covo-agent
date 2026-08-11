package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	activeProfileFile = "active_profile"
	profilesDir       = "profiles"
)

var activeProfileName string

func SetActiveProfile(name string) {
	activeProfileName = name
}

func ActiveProfileName() string {
	return activeProfileName
}

func ResolveActiveProfile() string {
	if activeProfileName != "" {
		return activeProfileName
	}
	if v := os.Getenv("COVO_PROFILE"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	ap := filepath.Join(home, ".covo-agent", activeProfileFile)
	data, err := os.ReadFile(ap)
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(data))
	if name == "" {
		return ""
	}
	return name
}

func HomeDirWithProfile(profile string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	base := filepath.Join(home, ".covo-agent")
	if profile == "" {
		return base, nil
	}
	return filepath.Join(base, profilesDir, profile), nil
}

func ProfileHomeDir(name string) (string, error) {
	return HomeDirWithProfile(name)
}

func ActiveProfilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".covo-agent", activeProfileFile), nil
}

func ListProfiles() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	pDir := filepath.Join(home, ".covo-agent", profilesDir)
	entries, err := os.ReadDir(pDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read profiles dir: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

func CreateProfile(name string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	pDir := filepath.Join(home, ".covo-agent", profilesDir, name)
	if err := os.MkdirAll(pDir, 0755); err != nil {
		return fmt.Errorf("create profile dir: %w", err)
	}
	cfg := DefaultConfig()
	if err := SaveConfigTo(cfg, pDir); err != nil {
		return fmt.Errorf("save profile config: %w", err)
	}
	return nil
}

func DeleteProfile(name string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	pDir := filepath.Join(home, ".covo-agent", profilesDir, name)
	if err := os.RemoveAll(pDir); err != nil {
		return fmt.Errorf("delete profile: %w", err)
	}
	if ResolveActiveProfile() == name {
		ap, _ := ActiveProfilePath()
		os.WriteFile(ap, []byte(""), 0644)
	}
	return nil
}

func UseProfile(name string) error {
	ap, err := ActiveProfilePath()
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	pDir := filepath.Join(home, ".covo-agent", profilesDir, name)
	if _, err := os.Stat(pDir); os.IsNotExist(err) {
		return fmt.Errorf("profile %q does not exist", name)
	}
	return os.WriteFile(ap, []byte(name+"\n"), 0644)
}

func SaveConfigTo(cfg *Config, dir string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	return os.WriteFile(cfgPath, data, 0644)
}

func EnsureProfileHomeDir(name string) (string, error) {
	home, err := ProfileHomeDir(name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(home, 0755); err != nil {
		return "", fmt.Errorf("create profile home: %w", err)
	}
	return home, nil
}

func PrintProfileStatus(name string) {
	home, _ := HomeDirWithProfile(name)
	fmt.Printf("  Name:      %s\n", name)
	fmt.Printf("  Home:      %s\n", home)
	cfgPath := filepath.Join(home, "config.yaml")
	if _, err := os.Stat(cfgPath); err == nil {
		fmt.Printf("  Config:    %s\n", cfgPath)
	} else {
		fmt.Printf("  Config:    %s (not found)\n", cfgPath)
	}
	envPath := filepath.Join(home, ".env")
	if _, err := os.Stat(envPath); err == nil {
		fmt.Printf("  .env:      %s\n", envPath)
	}
	sessionsPath := filepath.Join(home, "sessions")
	if _, err := os.Stat(sessionsPath); err == nil {
		fmt.Printf("  Sessions:  %s\n", sessionsPath)
	}
	skillsPath := filepath.Join(home, "skills")
	if _, err := os.Stat(skillsPath); err == nil {
		fmt.Printf("  Skills:    %s\n", skillsPath)
	}
}
