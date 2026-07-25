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
	GitHub   gogogo.Repository `json:"github"`
	Defaults map[string]string `json:"defaults"`
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
