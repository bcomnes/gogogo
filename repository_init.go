package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	gogogo "github.com/bcomnes/gogogo/pkg"
)

type projectCommandRunner interface {
	LookPath(name string) (string, error)
	Run(ctx context.Context, dir, name string, args ...string) ([]byte, error)
}

type execProjectCommandRunner struct{}

func (execProjectCommandRunner) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func (execProjectCommandRunner) Run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir
	return command.CombinedOutput()
}

func (a *application) initializeRepository(ctx context.Context, project gogogo.Project, opts options, output io.Writer) error {
	if err := ensureGitMetadataAbsent(project.Destination); err != nil {
		return err
	}

	runner := a.projectCommands()
	if commandOutput, err := runner.Run(ctx, project.Destination, "git", "init"); err != nil {
		return projectCommandContextError(ctx, "initialize Git repository", commandOutput, err)
	}
	fmt.Fprintln(output, "Initialized Git repository")

	if commandOutput, err := runner.Run(ctx, project.Destination, "git", "add", "--all"); err != nil {
		return projectCommandContextError(ctx, "stage project files", commandOutput, err)
	}
	if commandOutput, err := runner.Run(ctx, project.Destination, "git", "commit", "--allow-empty", "-m", "Initial commit"); err != nil {
		return projectCommandContextError(ctx, "create initial commit", commandOutput, err)
	}
	fmt.Fprintln(output, "Created initial commit")

	if opts.github == "" {
		return nil
	}
	if _, err := runner.LookPath("gh"); err != nil {
		fmt.Fprintln(output, "Warning: GitHub repository was not created because gh was not found.")
		fmt.Fprintln(output, "Install GitHub CLI from https://cli.github.com, then run gh repo create from the project directory.")
		return nil
	}

	authOutput, err := runner.Run(ctx, project.Destination, "gh", "auth", "status", "--hostname", "github.com")
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		fmt.Fprintln(output, "Warning: GitHub repository was not created because gh is not authenticated.")
		fmt.Fprintln(output, "Run gh auth login, then run gh repo create from the project directory.")
		if detail := strings.TrimSpace(string(authOutput)); detail != "" {
			fmt.Fprintf(output, "gh: %s\n", detail)
		}
		return nil
	}

	repository := project.Name
	if opts.githubOwner != "" {
		repository = opts.githubOwner + "/" + repository
	}
	args := []string{"repo", "create", repository, "--" + opts.github, "--source=.", "--remote=origin", "--push"}
	createOutput, err := runner.Run(ctx, project.Destination, "gh", args...)
	if err != nil {
		return projectCommandContextError(ctx, "create GitHub repository "+repository, createOutput, err)
	}
	fmt.Fprintf(output, "Created %s GitHub repository %s\n", opts.github, repository)
	if repositoryURL := strings.TrimSpace(string(createOutput)); repositoryURL != "" {
		fmt.Fprintln(output, repositoryURL)
	}
	return nil
}

func (a *application) projectCommands() projectCommandRunner {
	if a.commands != nil {
		return a.commands
	}
	return execProjectCommandRunner{}
}

func projectCommandContextError(ctx context.Context, action string, output []byte, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if detail := strings.TrimSpace(string(output)); detail != "" {
		return fmt.Errorf("%s: %w: %s", action, err, detail)
	}
	return fmt.Errorf("%s: %w", action, err)
}

func ensureGitMetadataAbsent(destination string) error {
	metadata := filepath.Join(destination, ".git")
	_, err := os.Lstat(metadata)
	if err == nil {
		return fmt.Errorf("refusing to initialize Git because the template contains %s", metadata)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect Git metadata: %w", err)
	}
	return nil
}
