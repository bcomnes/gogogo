package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bcomnes/gogogo/pkg"
)

const commandName = "gogogo"

// application owns CLI dependencies; injected clients and runners keep tests offline.
type application struct {
	client          *http.Client
	configPath      string
	configPathError error
	commands        projectCommandRunner
}

// options contains explicit command-line values before configured defaults are applied.
type options struct {
	configure     bool
	help          bool
	version       bool
	file          string
	url           string
	template      string
	pickTemplate  bool
	github        string
	githubOwner   string
	defaultGitHub string
	noGit         bool
	noGitHub      bool
	values        parameterFlags
	positionals   []string
}

type parameterFlags map[string]string

func (parameters *parameterFlags) String() string {
	if parameters == nil || len(*parameters) == 0 {
		return ""
	}
	keys := slices.Sorted(maps.Keys(*parameters))

	assignments := make([]string, 0, len(keys))
	for _, key := range keys {
		assignments = append(assignments, key+"="+(*parameters)[key])
	}
	return strings.Join(assignments, ",")
}

func (parameters *parameterFlags) Set(assignment string) error {
	key, value, found := strings.Cut(assignment, "=")
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if !found || key == "" || value == "" {
		return fmt.Errorf("parameter %q is malformed (expected key=value)", assignment)
	}
	if *parameters == nil {
		*parameters = make(parameterFlags)
	}
	(*parameters)[key] = value
	return nil
}

func newApplication() *application {
	configPath, err := defaultConfigPath()
	return &application{
		client:          &http.Client{Timeout: 5 * time.Minute},
		configPath:      configPath,
		configPathError: err,
	}
}

func (a *application) run(ctx context.Context, arguments []string, input io.Reader, output, errorOutput io.Writer) int {
	if len(arguments) > 0 && arguments[0] == "config" {
		return a.runConfig(arguments[1:], input, output, errorOutput)
	}

	opts, err := parseOptions(arguments)
	if err != nil {
		fmt.Fprintf(errorOutput, "Error: %v\n\n", err)
		printUsage(errorOutput, defaultConfig())
		return 2
	}
	if opts.version {
		fmt.Fprintf(output, "%s CLI version %s\n", commandName, Version)
		return 0
	}

	cfg := defaultConfig()
	if a.configPathError == nil {
		cfg, err = loadConfig(a.configPath)
		if err != nil {
			fmt.Fprintf(errorOutput, "Error: %v\n", err)
			return 1
		}
	}
	if opts.help {
		printUsage(output, cfg)
		return 0
	}
	if a.configPathError != nil {
		fmt.Fprintf(errorOutput, "Error: %v\n", a.configPathError)
		return 1
	}
	if opts.defaultGitHub != "" {
		if opts.configure || opts.hasProjectArguments() {
			fmt.Fprintln(errorOutput, "Error: -default-github cannot be combined with other project or configuration options")
			return 2
		}
		visibility, err := parseConfiguredGitHubVisibility(opts.defaultGitHub)
		if err != nil {
			fmt.Fprintf(errorOutput, "Error: %v\n", err)
			return 2
		}
		cfg.GitHubVisibility = visibility
		if err := saveConfig(a.configPath, cfg); err != nil {
			fmt.Fprintf(errorOutput, "Error: %v\n", err)
			return 1
		}
		if visibility == "" {
			fmt.Fprintln(output, "Default GitHub repository creation disabled")
		} else {
			fmt.Fprintf(output, "Default GitHub visibility set to %s\n", visibility)
		}
		return 0
	}
	if opts.configure {
		if opts.hasProjectArguments() {
			fmt.Fprintln(errorOutput, "Error: -configure cannot be combined with project arguments")
			return 2
		}
		if err := configure(input, output, a.configPath, cfg); err != nil {
			fmt.Fprintf(errorOutput, "Error: %v\n", err)
			return 1
		}
		return 0
	}
	if len(opts.positionals) == 0 {
		fmt.Fprintln(errorOutput, "Error: <name> positional argument is required")
		printUsage(errorOutput, cfg)
		return 2
	}

	opts, err = resolveGitHubOptions(opts, cfg)
	if err != nil {
		fmt.Fprintf(errorOutput, "Error: %v\n", err)
		return 2
	}
	cfg, err = selectTemplate(ctx, opts, cfg, input, output)
	if err != nil {
		fmt.Fprintf(errorOutput, "Error: %v\n", err)
		if errors.Is(err, context.Canceled) {
			return 130
		}
		return 1
	}
	if err := a.createProject(ctx, opts, cfg, output, errorOutput); err != nil {
		fmt.Fprintf(errorOutput, "Error: %v\n", err)
		if errors.Is(err, context.Canceled) {
			return 130
		}
		return 1
	}
	return 0
}

// createProject publishes the extracted template before running optional Git setup.
// Repository setup failures leave the generated files available for recovery.
func (a *application) createProject(ctx context.Context, opts options, cfg config, output, errorOutput io.Writer) error {
	if len(opts.positionals) > 2 {
		return fmt.Errorf("too many positional arguments")
	}
	if (opts.file != "" || opts.url != "") && len(opts.positionals) > 1 {
		return fmt.Errorf("a repository cannot be combined with -file or -url")
	}

	destination := opts.positionals[0]
	projectName := filepath.Base(filepath.Clean(destination))

	source, sourceLabel, err := a.openTemplate(ctx, opts, cfg, output)
	if err != nil {
		return err
	}
	defer source.Close()

	parameters := make(map[string]string, len(cfg.Defaults)+len(opts.values))
	maps.Copy(parameters, cfg.Defaults)
	maps.Copy(parameters, opts.values)

	fmt.Fprintf(output, "Creating new project %s from %s\n", projectName, sourceLabel)
	project, err := gogogo.Create(ctx, destination, source, gogogo.Options{Parameters: parameters})
	if err != nil {
		return fmt.Errorf("create project: %w", err)
	}
	fmt.Fprintf(output, "Project created in %s\n", project.Destination)
	if opts.noGit {
		return nil
	}
	if err := a.initializeRepository(ctx, project, opts, output, errorOutput); err != nil {
		return fmt.Errorf("initialize project repository: %w", err)
	}
	return nil
}

// openTemplate opens a local archive or downloads the selected repository/URL.
// The returned stream belongs to the caller and must be closed.
func (a *application) openTemplate(ctx context.Context, opts options, cfg config, output io.Writer) (io.ReadCloser, string, error) {
	if opts.file != "" {
		source, err := os.Open(opts.file)
		if err != nil {
			return nil, "", fmt.Errorf("open template %s: %w", opts.file, err)
		}
		return source, opts.file, nil
	}

	label, archiveURL := opts.url, opts.url
	if archiveURL == "" {
		repo := cfg.GitHub
		if len(opts.positionals) == 2 {
			argument := opts.positionals[1]
			if strings.Contains(argument, "/") {
				var err error
				repo, err = gogogo.ParseRepository(argument)
				if err != nil {
					return nil, "", fmt.Errorf("parse repository: %w", err)
				}
			} else {
				repo.Branch = argument
			}
		}
		label, archiveURL = repo.String(), repo.ArchiveURL()
	}

	fmt.Fprintf(output, "Downloading template from %s...\n", label)
	source, err := a.openURL(ctx, archiveURL)
	if err != nil {
		return nil, "", fmt.Errorf("open template %s: %w", label, err)
	}
	return source, label, nil
}

func (a *application) openURL(ctx context.Context, value string) (io.ReadCloser, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("URL scheme must be http or https")
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("User-Agent", commandName+"/"+Version)

	response, err := a.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4*1024))
		if body := strings.TrimSpace(string(message)); body != "" {
			return nil, fmt.Errorf("fetch failed with %s: %s", response.Status, body)
		}
		return nil, fmt.Errorf("fetch failed with %s", response.Status)
	}
	return response.Body, nil
}

// parseOptions rejects conflicting explicit sources before prompting or doing I/O.
func parseOptions(arguments []string) (options, error) {
	opts := options{values: make(parameterFlags)}
	flags := newFlagSet(&opts, io.Discard)
	if err := flags.Parse(arguments); err != nil {
		return options{}, err
	}
	var selectionError error
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "template" {
			selectionError = validateTemplateName(opts.template)
		}
	})
	if selectionError != nil {
		return options{}, fmt.Errorf("-template: %w", selectionError)
	}
	opts.positionals = flags.Args()
	for _, argument := range opts.positionals {
		if strings.HasPrefix(argument, "-") {
			return options{}, fmt.Errorf("flags must be specified before the project name")
		}
	}
	if opts.template != "" && opts.pickTemplate {
		return options{}, fmt.Errorf("-template and -pick-template are mutually exclusive")
	}
	if (opts.template != "" || opts.pickTemplate) && (opts.file != "" || opts.url != "" || len(opts.positionals) > 1) {
		return options{}, fmt.Errorf("template selection cannot be combined with -file, -url, or a positional repository")
	}
	if opts.file != "" && opts.url != "" {
		return options{}, fmt.Errorf("-file and -url are mutually exclusive")
	}
	if opts.noGit && opts.github != "" {
		return options{}, fmt.Errorf("-no-git and -github are mutually exclusive")
	}
	if opts.noGitHub && opts.github != "" {
		return options{}, fmt.Errorf("-no-github and -github are mutually exclusive")
	}
	switch opts.github {
	case "", "private", "public", "internal":
	default:
		return options{}, fmt.Errorf("-github must be private, public, or internal")
	}
	return opts, nil
}

func (opts options) hasProjectArguments() bool {
	return len(opts.positionals) != 0 || opts.file != "" || opts.url != "" || opts.template != "" || opts.pickTemplate ||
		opts.github != "" || opts.githubOwner != "" || opts.noGit || opts.noGitHub || len(opts.values) != 0
}

// resolveGitHubOptions applies saved defaults without overriding per-run opt-outs.
// A configured owner alone never enables remote repository creation.
func resolveGitHubOptions(opts options, cfg config) (options, error) {
	switch {
	case opts.noGit || opts.noGitHub:
		opts.github = ""
	case opts.github == "":
		opts.github = cfg.GitHubVisibility
	}
	if opts.githubOwner != "" && opts.github == "" {
		return options{}, fmt.Errorf("-github-owner requires GitHub repository creation")
	}
	if opts.github != "" && opts.githubOwner == "" {
		opts.githubOwner = cfg.GitHubOwner
	}
	return opts, nil
}

func newFlagSet(opts *options, output io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(commandName, flag.ContinueOnError)
	flags.SetOutput(output)
	flags.BoolVar(&opts.configure, "configure", false, "Set the default repository, GitHub visibility, and parameters")
	flags.StringVar(&opts.defaultGitHub, "default-github", "", "Set default GitHub visibility and exit: none, private, public, or internal")
	flags.StringVar(&opts.file, "file", "", "Read a local .tar or .tar.gz template")
	flags.StringVar(&opts.github, "github", "", "Create and push a GitHub repository: private, public, or internal")
	flags.StringVar(&opts.githubOwner, "github-owner", "", "GitHub user or organization; defaults to the authenticated user")
	flags.BoolVar(&opts.help, "help", false, "Show this help message and exit")
	flags.BoolVar(&opts.noGit, "no-git", false, "Do not initialize a local Git repository")
	flags.BoolVar(&opts.noGitHub, "no-github", false, "Do not create a GitHub repository for this project")
	flags.StringVar(&opts.template, "template", "", "Use a saved template by name")
	flags.BoolVar(&opts.pickTemplate, "pick-template", false, "Choose a template interactively before creating the project")
	flags.Var(&opts.values, "set", "Set a template parameter as key=value; may be repeated")
	flags.StringVar(&opts.url, "url", "", "Download a .tar or .tar.gz template")
	flags.BoolVar(&opts.version, "version", false, "Show the CLI version and exit")
	return flags
}

func printUsage(output io.Writer, cfg config) {
	fmt.Fprintf(output, `Usage:
  gogogo [options] <name> [%s]
  gogogo config <show|get|path|set|unset|validate|reset> ...

Create a project from a GitHub repository, local tar archive, or URL.
Flags must be specified before the project name.

Examples:
  gogogo my-project
  gogogo -set owner=bcomnes my-project
  gogogo -github=private my-project
  gogogo -github=public -github-owner=my-org my-project
  gogogo config set github.visibility private
  gogogo config show
  gogogo -template=cli my-project
  gogogo -pick-template my-project
  gogogo my-project owner/template#main
  gogogo -file template.tar.gz my-project

Positional arguments:
  <name>          Destination directory and default name parameter
  [repository]    GitHub user/repo[#branch], or a branch of the configured repository

Options:
`, cfg.GitHub.String())

	opts := options{values: make(parameterFlags)}
	flags := newFlagSet(&opts, output)
	flags.PrintDefaults()

	visibility := cfg.GitHubVisibility
	if visibility == "" {
		visibility = "none"
	}
	fmt.Fprintf(output, "\nConfigured GitHub visibility: %s\n", visibility)
	owner := cfg.GitHubOwner
	if owner == "" {
		owner = "authenticated user"
	}
	fmt.Fprintf(output, "Configured GitHub owner: %s\n", owner)

	if len(cfg.Defaults) > 0 {
		keys := slices.Sorted(maps.Keys(cfg.Defaults))
		fmt.Fprintln(output)
		fmt.Fprintln(output, "Default parameters:")
		for _, key := range keys {
			fmt.Fprintf(output, "  %s=%s\n", key, cfg.Defaults[key])
		}
	}
}
