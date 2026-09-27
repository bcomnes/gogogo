package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bcomnes/gogogo/pkg"
)

// config is the persisted configuration, overlaid on built-in defaults when loaded.
// GitHub selects the default template; Templates is an independent inventory of
// named repositories and does not change that selection. Older files may omit
// the inventory entirely. Default parameter values remain in Defaults.
type config struct {
	GitHub           gogogo.Repository `json:"github"`
	GitHubVisibility string            `json:"github_visibility,omitempty"`
	GitHubOwner      string            `json:"github_owner,omitempty"`
	Defaults         map[string]string `json:"defaults"`
	// Templates maps case-sensitive names to canonical owner/repo#branch references.
	Templates map[string]string `json:"templates,omitempty"`
}

// defaultConfig returns fresh configuration with independent, writable maps.
// A malformed built-in repository is a programming error rather than a user
// configuration error, so parsing it unsuccessfully panics.
func defaultConfig() config {
	repo, err := gogogo.ParseRepository(gogogo.DefaultRepository)
	if err != nil {
		panic(err)
	}

	return config{
		GitHub:    repo,
		Defaults:  make(map[string]string),
		Templates: make(map[string]string),
	}
}

// defaultConfigPath honors GOGOGO_CONFIG verbatim when set, otherwise locating
// gogogo.json under the user's ~/.config directory. It does not create files.
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

// loadConfig overlays one JSON value on the built-in defaults and validates the
// resulting configuration. A missing file is equivalent to fresh defaults;
// missing or null maps become writable empty maps. Inventory references are
// canonicalized in memory without rewriting the source file.
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
	if cfg.Templates == nil {
		cfg.Templates = make(map[string]string)
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Templates)) {
		if err := validateTemplateName(name); err != nil {
			return config{}, err
		}
		repo, err := gogogo.ParseRepository(cfg.Templates[name])
		if err != nil {
			return config{}, fmt.Errorf("invalid template %q: %w", name, err)
		}
		cfg.Templates[name] = repo.String()
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

// validateTemplateName checks the shared naming contract for inventory keys.
// Names are nonempty and case-sensitive, with only ASCII letters, digits,
// underscores, and hyphens allowed. Whitespace is rejected, not trimmed, so a
// name has the same meaning in stored JSON and template.<name> config commands.
func validateTemplateName(name string) error {
	if name != "" && strings.IndexFunc(name, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-')
	}) == -1 {
		return nil
	}
	return fmt.Errorf("invalid template name %q (expected nonempty ASCII letters, digits, underscores, or hyphens)", name)
}

// ensureJSONEnd permits trailing whitespace but rejects a second JSON value or
// malformed trailing content, preventing partially accepted configuration files.
func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("unexpected data after JSON object")
}

// saveConfig writes indented JSON through a private temporary file in the same
// directory, then renames it over the destination. Callers supply validated
// configuration; failed writes leave the previous file intact and clean up the
// temporary file. Empty inventories are omitted for backward compatibility.
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

// configure runs the legacy interactive setup for the default repository,
// GitHub settings, and parameter defaults, preserving the named inventory.
// EOF ends further prompts, and only a successfully completed setup is saved.
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

	keys := slices.Sorted(maps.Keys(cfg.Defaults))

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

// parseConfiguredGitHubVisibility normalizes a visibility setting and maps
// "none" to the empty value used to disable automatic GitHub creation.
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

// parseConfiguredGitHubOwner accepts one repository-owner component, using the
// repository parser's validation rules. Empty input or "none" selects the
// authenticated user rather than an explicitly configured owner.
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

// prompt reads a trimmed line, substituting defaultValue for a blank response.
// It reports EOF separately from errors so callers can use a final unterminated
// response while avoiding subsequent prompts on an exhausted input stream.
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
