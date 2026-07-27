package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bcomnes/gogogo/pkg"
)

type config struct {
	GitHub           gogogo.Repository `json:"github"`
	GitHubVisibility string            `json:"github_visibility,omitempty"`
	GitHubOwner      string            `json:"github_owner,omitempty"`
	Defaults         map[string]string `json:"defaults"`
}

func defaultConfig() config {
	repo, err := gogogo.ParseRepository(gogogo.DefaultRepository)
	if err != nil {
		panic(err)
	}

	return config{
		GitHub:   repo,
		Defaults: make(map[string]string),
	}
}

func defaultConfigPath() (string, error) {
	if path := os.Getenv("GOGOGO_CONFIG"); path != "" {
		return path, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".config", "gogogo.json"), nil
}

func loadConfig(path string) (config, error) {
	cfg := defaultConfig()
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return config{}, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&cfg); err != nil {
		return config{}, fmt.Errorf("decode config: %w", err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return config{}, fmt.Errorf("decode config: %w", err)
	}
	if cfg.Defaults == nil {
		cfg.Defaults = make(map[string]string)
	}
	if _, err := gogogo.ParseRepository(cfg.GitHub.String()); err != nil {
		return config{}, fmt.Errorf("invalid configured repository: %w", err)
	}
	visibility, err := parseConfiguredGitHubVisibility(cfg.GitHubVisibility)
	if err != nil {
		return config{}, err
	}
	cfg.GitHubVisibility = visibility
	owner, err := parseConfiguredGitHubOwner(cfg.GitHubOwner)
	if err != nil {
		return config{}, err
	}
	cfg.GitHubOwner = owner

	return cfg, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("unexpected data after JSON object")
}

func saveConfig(path string, cfg config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	file, err := os.CreateTemp(filepath.Dir(path), ".gogogo-*.json")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	temporaryPath := file.Name()
	defer os.Remove(temporaryPath)

	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return fmt.Errorf("set config permissions: %w", err)
	}

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(cfg); err != nil {
		file.Close()
		return fmt.Errorf("encode config: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("store config: %w", err)
	}
	return nil
}

func configure(input io.Reader, output io.Writer, path string, cfg config) error {
	reader := bufio.NewReader(input)

	line, eof, err := prompt(reader, output, "Set repository", cfg.GitHub.String())
	if err != nil {
		return err
	}
	if line != "" {
		repo, err := gogogo.ParseRepository(line)
		if err != nil {
			return err
		}
		cfg.GitHub = repo
	}

	if !eof {
		defaultVisibility := cfg.GitHubVisibility
		if defaultVisibility == "" {
			defaultVisibility = "none"
		}
		line, reachedEOF, err := prompt(reader, output, "Default GitHub visibility (none/private/public/internal)", defaultVisibility)
		if err != nil {
			return err
		}
		eof = reachedEOF
		visibility, err := parseConfiguredGitHubVisibility(line)
		if err != nil {
			return err
		}
		cfg.GitHubVisibility = visibility
	}

	if !eof {
		defaultOwner := cfg.GitHubOwner
		if defaultOwner == "" {
			defaultOwner = "none"
		}
		line, reachedEOF, err := prompt(reader, output, "Default GitHub owner (none for authenticated user)", defaultOwner)
		if err != nil {
			return err
		}
		eof = reachedEOF
		owner, err := parseConfiguredGitHubOwner(line)
		if err != nil {
			return err
		}
		cfg.GitHubOwner = owner
	}

	keys := make([]string, 0, len(cfg.Defaults))
	for key := range cfg.Defaults {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for index := 0; !eof; index++ {
		defaultValue := ""
		if index < len(keys) {
			key := keys[index]
			defaultValue = key + "=" + cfg.Defaults[key]
		}

		line, reachedEOF, err := prompt(reader, output, "Set key=value (blank to finish)", defaultValue)
		if err != nil {
			return err
		}
		eof = reachedEOF
		if line == "" {
			break
		}

		key, value, found := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !found || key == "" {
			return fmt.Errorf("parameter %q is malformed (expected key=value)", line)
		}
		if value == "" {
			delete(cfg.Defaults, key)
		} else {
			cfg.Defaults[key] = value
		}
	}

	if err := saveConfig(path, cfg); err != nil {
		return err
	}
	fmt.Fprintf(output, "Configuration saved to %s\n", path)
	return nil
}

func parseConfiguredGitHubVisibility(value string) (string, error) {
	visibility := strings.ToLower(strings.TrimSpace(value))
	switch visibility {
	case "", "none":
		return "", nil
	case "private", "public", "internal":
		return visibility, nil
	default:
		return "", fmt.Errorf("GitHub visibility must be none, private, public, or internal")
	}
}

func parseConfiguredGitHubOwner(value string) (string, error) {
	owner := strings.TrimSpace(value)
	if owner == "" || strings.EqualFold(owner, "none") {
		return "", nil
	}
	repo, err := gogogo.ParseRepository(owner + "/repository")
	if err != nil || repo.User != owner {
		return "", fmt.Errorf("GitHub owner %q is invalid", value)
	}
	return owner, nil
}

func prompt(reader *bufio.Reader, output io.Writer, label, defaultValue string) (string, bool, error) {
	fmt.Fprint(output, label)
	if defaultValue != "" {
		fmt.Fprintf(output, " [%s]", defaultValue)
	}
	fmt.Fprint(output, ": ")

	line, err := reader.ReadString('\n')
	eof := errors.Is(err, io.EOF)
	if err != nil && !eof {
		return "", false, fmt.Errorf("read input: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		line = defaultValue
	}
	return line, eof, nil
}
