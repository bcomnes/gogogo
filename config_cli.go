package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	gogogo "github.com/bcomnes/gogogo/pkg"
)

func (a *application) runConfig(arguments []string, output, errorOutput io.Writer) int {
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

	default:
		return configUsageError(errorOutput, fmt.Sprintf("unknown config command %q", arguments[0]))
	}
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

	parameter, found := strings.CutPrefix(key, "parameter.")
	if !found || strings.TrimSpace(parameter) == "" {
		return fmt.Errorf("unknown config key %q", key)
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

	parameter, found := strings.CutPrefix(key, "parameter.")
	if !found || strings.TrimSpace(parameter) == "" {
		return fmt.Errorf("unknown config key %q", key)
	}
	delete(cfg.Defaults, parameter)
	return nil
}

func configUsageError(output io.Writer, message string) int {
	fmt.Fprintf(output, "Error: %s\n\n", message)
	printConfigUsage(output)
	return 2
}

func printConfigUsage(output io.Writer) {
	fmt.Fprint(output, `Usage:
  gogogo config show
  gogogo config path
  gogogo config set <key> <value>
  gogogo config unset <key>

Keys:
  template              Template repository as owner/repo[#branch]
  github.visibility     none, private, public, or internal
  github.owner          GitHub user or organization; none uses the authenticated user
  parameter.<name>      Default template parameter
`)
}
