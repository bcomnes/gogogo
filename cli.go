package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bcomnes/gogogo/pkg"
)

const commandName = "gogogo"

type application struct {
	client          *http.Client
	configPath      string
	configPathError error
	commands        projectCommandRunner
}

type options struct {
	configure   bool
	help        bool
	version     bool
	file        string
	url         string
	github      string
	githubOwner string
	noGit       bool
	values      parameterFlags
	positionals []string
}

type parameterFlags map[string]string

func (parameters *parameterFlags) String() string {
	if parameters == nil || len(*parameters) == 0 {
		return ""
	}
	keys := make([]string, 0, len(*parameters))
	for key := range *parameters {
		keys = append(keys, key)
	}
	sort.Strings(keys)

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
		commands:        execProjectCommandRunner{},
	}
}

func (a *application) run(ctx context.Context, arguments []string, input io.Reader, output, errorOutput io.Writer) int {
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
	if opts.configure {
		if len(opts.positionals) != 0 || opts.file != "" || opts.url != "" || len(opts.values) != 0 {
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

	if err := a.createProject(ctx, opts, cfg, output, errorOutput); err != nil {
		fmt.Fprintf(errorOutput, "Error: %v\n", err)
		if errors.Is(err, context.Canceled) {
			return 130
		}
		return 1
	}
	return 0
}

func (a *application) createProject(ctx context.Context, opts options, cfg config, output, errorOutput io.Writer) error {
	if len(opts.positionals) > 2 {
		return fmt.Errorf("too many positional arguments")
	}
	if (opts.file != "" || opts.url != "") && len(opts.positionals) > 1 {
		return fmt.Errorf("a repository cannot be combined with -file or -url")
	}

	destination := opts.positionals[0]
	projectName := filepath.Base(filepath.Clean(destination))

	var source io.ReadCloser
	var sourceLabel string
	var err error
	switch {
	case opts.file != "":
		source, err = os.Open(opts.file)
		sourceLabel = opts.file
	case opts.url != "":
		sourceLabel = opts.url
		fmt.Fprintf(output, "Downloading template from %s...\n", sourceLabel)
		source, err = a.openURL(ctx, opts.url)
	default:
		repo := cfg.GitHub
		if len(opts.positionals) == 2 {
			repositoryArgument := opts.positionals[1]
			if !strings.Contains(repositoryArgument, "/") {
				repo.Branch = repositoryArgument
			} else {
				repo, err = gogogo.ParseRepository(repositoryArgument)
				if err != nil {
					return fmt.Errorf("parse repository: %w", err)
				}
			}
		}
		sourceLabel = repo.String()
		fmt.Fprintf(output, "Downloading template from %s...\n", sourceLabel)
		source, err = a.openURL(ctx, repo.ArchiveURL())
	}
	if err != nil {
		return fmt.Errorf("open template %s: %w", sourceLabel, err)
	}
	defer source.Close()

	parameters := make(map[string]string, len(cfg.Defaults)+len(opts.values))
	for key, value := range cfg.Defaults {
		parameters[key] = value
	}
	for key, value := range opts.values {
		parameters[key] = value
	}

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

func parseOptions(arguments []string) (options, error) {
	opts := options{values: make(parameterFlags)}
	flags := newFlagSet(&opts, io.Discard)
	if err := flags.Parse(arguments); err != nil {
		return options{}, err
	}
	opts.positionals = flags.Args()
	for _, argument := range opts.positionals {
		if strings.HasPrefix(argument, "-") {
			return options{}, fmt.Errorf("flags must be specified before the project name")
		}
	}
	if opts.file != "" && opts.url != "" {
		return options{}, fmt.Errorf("-file and -url are mutually exclusive")
	}
	if opts.noGit && opts.github != "" {
		return options{}, fmt.Errorf("-no-git and -github are mutually exclusive")
	}
	if opts.githubOwner != "" && opts.github == "" {
		return options{}, fmt.Errorf("-github-owner requires -github")
	}
	switch opts.github {
	case "", "private", "public", "internal":
	default:
		return options{}, fmt.Errorf("-github must be private, public, or internal")
	}
	return opts, nil
}

func newFlagSet(opts *options, output io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(commandName, flag.ContinueOnError)
	flags.SetOutput(output)
	flags.BoolVar(&opts.configure, "configure", false, "Set the default repository and parameters")
	flags.StringVar(&opts.file, "file", "", "Read a local .tar or .tar.gz template")
	flags.StringVar(&opts.github, "github", "", "Create and push a GitHub repository: private, public, or internal")
	flags.StringVar(&opts.githubOwner, "github-owner", "", "GitHub user or organization; defaults to the authenticated user")
	flags.BoolVar(&opts.help, "help", false, "Show this help message and exit")
	flags.BoolVar(&opts.noGit, "no-git", false, "Do not initialize a local Git repository")
	flags.Var(&opts.values, "set", "Set a template parameter as key=value; may be repeated")
	flags.StringVar(&opts.url, "url", "", "Download a .tar or .tar.gz template")
	flags.BoolVar(&opts.version, "version", false, "Show the CLI version and exit")
	return flags
}

func printUsage(output io.Writer, cfg config) {
	fmt.Fprintf(output, `Usage:
  gogogo [options] <name> [%s]

Create a project from a GitHub repository, local tar archive, or URL.
Flags must be specified before the project name.

Examples:
  gogogo my-project
  gogogo -set owner=bcomnes my-project
  gogogo -github=private my-project
  gogogo -github=public -github-owner=my-org my-project
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

	if len(cfg.Defaults) > 0 {
		keys := make([]string, 0, len(cfg.Defaults))
		for key := range cfg.Defaults {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		fmt.Fprintln(output)
		fmt.Fprintln(output, "Default parameters:")
		for _, key := range keys {
			fmt.Fprintf(output, "  %s=%s\n", key, cfg.Defaults[key])
		}
	}
}
