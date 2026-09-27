package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	gogogo "github.com/bcomnes/gogogo/pkg"
)

func (a *application) runConfig(arguments []string, input io.Reader, output, errorOutput io.Writer) int {
	if len(arguments) == 1 && (arguments[0] == "help" || arguments[0] == "-help" || arguments[0] == "--help") {
		printConfigUsage(output)
		return 0
	}
	if len(arguments) == 0 {
		fmt.Fprintln(errorOutput, "Error: config command is required")
		printConfigUsage(errorOutput)
		return 2
	}
	if a.configPathError != nil {
		fmt.Fprintf(errorOutput, "Error: %v\n", a.configPathError)
		return 1
	}

	switch arguments[0] {
	case "path":
		if len(arguments) != 1 {
			return configUsageError(errorOutput, "config path does not accept arguments")
		}
		fmt.Fprintln(output, a.configPath)
		return 0

	case "get":
		if len(arguments) != 2 {
			return configUsageError(errorOutput, "config get requires <key>")
		}
		cfg, err := loadConfig(a.configPath)
		if err != nil {
			fmt.Fprintf(errorOutput, "Error: %v\n", err)
			return 1
		}
		value, err := getConfigValue(cfg, arguments[1])
		if err != nil {
			fmt.Fprintf(errorOutput, "Error: %v\n", err)
			return 2
		}
		fmt.Fprintln(output, value)
		return 0

	case "show":
		if len(arguments) != 1 {
			return configUsageError(errorOutput, "config show does not accept arguments")
		}
		cfg, err := loadConfig(a.configPath)
		if err != nil {
			fmt.Fprintf(errorOutput, "Error: %v\n", err)
			return 1
		}
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(cfg); err != nil {
			fmt.Fprintf(errorOutput, "Error: encode config: %v\n", err)
			return 1
		}
		return 0

	case "set":
		if len(arguments) != 3 {
			return configUsageError(errorOutput, "config set requires <key> and <value>")
		}
		return a.updateConfig(arguments[1], arguments[2], false, output, errorOutput)

	case "unset":
		if len(arguments) != 2 {
			return configUsageError(errorOutput, "config unset requires <key>")
		}
		return a.updateConfig(arguments[1], "", true, output, errorOutput)

	case "validate":
		if len(arguments) != 1 {
			return configUsageError(errorOutput, "config validate does not accept arguments")
		}
		if _, err := loadConfig(a.configPath); err != nil {
			fmt.Fprintf(errorOutput, "Error: %v\n", err)
			return 1
		}
		fmt.Fprintf(output, "Configuration is valid: %s\n", a.configPath)
		return 0

	case "reset":
		force := len(arguments) == 2 && arguments[1] == "--force"
		if len(arguments) > 2 || (len(arguments) == 2 && !force) {
			return configUsageError(errorOutput, "config reset accepts only --force")
		}
		return a.resetConfig(force, input, output, errorOutput)

	default:
		return configUsageError(errorOutput, fmt.Sprintf("unknown config command %q", arguments[0]))
	}
}

func (a *application) resetConfig(force bool, input io.Reader, output, errorOutput io.Writer) int {
	if !force {
		fmt.Fprint(output, "Reset all configuration? [y/N]: ")
		answer, err := bufio.NewReader(input).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			fmt.Fprintf(errorOutput, "Error: read input: %v\n", err)
			return 1
		}
		answer = strings.TrimSpace(answer)
		if !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") {
			fmt.Fprintln(output, "Configuration unchanged")
			return 0
		}
	}
	if err := os.Remove(a.configPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(errorOutput, "Error: reset config: %v\n", err)
		return 1
	}
	fmt.Fprintf(output, "Configuration reset: %s\n", a.configPath)
	return 0
}

func (a *application) updateConfig(key, value string, unset bool, output, errorOutput io.Writer) int {
	cfg, err := loadConfig(a.configPath)
	if err != nil {
		fmt.Fprintf(errorOutput, "Error: %v\n", err)
		return 1
	}
	if unset {
		err = unsetConfigValue(&cfg, key)
	} else {
		err = setConfigValue(&cfg, key, value)
	}
	if err != nil {
		fmt.Fprintf(errorOutput, "Error: %v\n", err)
		return 2
	}
	if err := saveConfig(a.configPath, cfg); err != nil {
		fmt.Fprintf(errorOutput, "Error: %v\n", err)
		return 1
	}
	if unset {
		fmt.Fprintf(output, "Unset %s in %s\n", key, a.configPath)
	} else {
		fmt.Fprintf(output, "Set %s in %s\n", key, a.configPath)
	}
	return 0
}

func getConfigValue(cfg config, key string) (string, error) {
	switch key {
	case "template":
		return cfg.GitHub.String(), nil
	case "github.visibility":
		if cfg.GitHubVisibility == "" {
			return "none", nil
		}
		return cfg.GitHubVisibility, nil
	case "github.owner":
		if cfg.GitHubOwner == "" {
			return "none", nil
		}
		return cfg.GitHubOwner, nil
	}

	parameter, err := configParameterName(key)
	if err != nil {
		return "", err
	}
	value, found := cfg.Defaults[parameter]
	if !found {
		return "", fmt.Errorf("config key %q is not set", key)
	}
	return value, nil
}

func setConfigValue(cfg *config, key, value string) error {
	switch key {
	case "template":
		repo, err := gogogo.ParseRepository(value)
		if err != nil {
			return fmt.Errorf("invalid template: %w", err)
		}
		cfg.GitHub = repo
		return nil
	case "github.visibility":
		visibility, err := parseConfiguredGitHubVisibility(value)
		if err != nil {
			return err
		}
		cfg.GitHubVisibility = visibility
		return nil
	case "github.owner":
		owner, err := parseConfiguredGitHubOwner(value)
		if err != nil {
			return err
		}
		cfg.GitHubOwner = owner
		return nil
	}

	parameter, err := configParameterName(key)
	if err != nil {
		return err
	}
	if value == "" {
		return fmt.Errorf("parameter value cannot be empty; use config unset %s", key)
	}
	cfg.Defaults[parameter] = value
	return nil
}

func unsetConfigValue(cfg *config, key string) error {
	switch key {
	case "template":
		cfg.GitHub = defaultConfig().GitHub
		return nil
	case "github.visibility":
		cfg.GitHubVisibility = ""
		return nil
	case "github.owner":
		cfg.GitHubOwner = ""
		return nil
	}

	parameter, err := configParameterName(key)
	if err != nil {
		return err
	}
	delete(cfg.Defaults, parameter)
	return nil
}

func configParameterName(key string) (string, error) {
	parameter, found := strings.CutPrefix(key, "parameter.")
	if !found || strings.TrimSpace(parameter) == "" {
		return "", fmt.Errorf("unknown config key %q", key)
	}
	return parameter, nil
}

func configUsageError(output io.Writer, message string) int {
	fmt.Fprintf(output, "Error: %s\n\n", message)
	printConfigUsage(output)
	return 2
}

func printConfigUsage(output io.Writer) {
	fmt.Fprint(output, `Usage:
  gogogo config show
  gogogo config get <key>
  gogogo config path
  gogogo config set <key> <value>
  gogogo config unset <key>
  gogogo config validate
  gogogo config reset [--force]

Keys:
  template              Template repository as owner/repo[#branch]
  github.visibility     none, private, public, or internal
  github.owner          GitHub user or organization; none uses the authenticated user
  parameter.<name>      Default template parameter
`)
}
