package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	gogogo "github.com/bcomnes/gogogo/pkg"
)

// projectCommandRunner abstracts executable discovery and synchronous execution.
// Implementations run in the supplied directory, route output to the supplied
// writers, and should honor context cancellation without interpreting args as shell
// input. Callers use this boundary to substitute runners in tests.
type projectCommandRunner interface {
	LookPath(name string) (string, error)
	Run(ctx context.Context, dir string, stdout, stderr io.Writer, name string, args ...string) error
}

// Command deadlines bound individual steps, not the entire initialization. The
// parent context may impose a shorter deadline across the whole operation.
const (
	gitCommandTimeout = time.Minute
	ghAuthTimeout     = 30 * time.Second
	ghCreateTimeout   = 2 * time.Minute
)

// execProjectCommandRunner executes local tools with the process environment.
// It does not sandbox Git configuration, hooks, or GitHub CLI credentials.
type execProjectCommandRunner struct{}

// LookPath uses the process PATH to locate a tool; it does not verify its identity
// or guarantee that a later invocation will succeed.
func (execProjectCommandRunner) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

// Run invokes a program directly, without a shell, and waits for completion.
// CommandContext kills the command process when ctx is done; this does not promise
// termination of every descendant process or interruption of blocked output writers.
func (execProjectCommandRunner) Run(ctx context.Context, dir string, stdout, stderr io.Writer, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
}

// initializeRepository creates a local repository and initial commit, then
// optionally creates a GitHub repository and pushes through the authenticated gh
// CLI. opts must already contain validated GitHub visibility and owner settings.
// All project files eligible for git add --all may be included in the push; this
// function does not inspect them for secrets or override local Git behavior.
//
// Existing .git entries are rejected before any command runs. Subsequent failures
// are not rolled back: files, commits, remotes, or a remote repository may remain.
// Missing gh or a non-cancellation authentication failure produces a warning and
// leaves the successful local initialization intact. Cancellation and timeouts are
// errors, as are failures during repository creation or push.
func (a *application) initializeRepository(ctx context.Context, project gogogo.Project, opts options, output, errorOutput io.Writer) error {
	if err := ensureGitMetadataAbsent(project.Destination); err != nil {
		return err
	}

	runner := a.projectCommands()
	run := func(timeout time.Duration, action, name string, args ...string) error {
		return runProjectCommand(ctx, runner, timeout, action, project.Destination, output, errorOutput, name, args...)
	}

	fmt.Fprintln(output, "Initializing Git repository...")
	if err := run(gitCommandTimeout, "initialize Git repository", "git", "init"); err != nil {
		return err
	}
	fmt.Fprintln(output, "Initialized Git repository")

	fmt.Fprintln(output, "Creating initial commit...")
	if err := run(gitCommandTimeout, "stage project files", "git", "add", "--all"); err != nil {
		return err
	}
	if err := run(gitCommandTimeout, "create initial commit", "git", "commit", "--allow-empty", "-m", "Initial commit"); err != nil {
		return err
	}
	fmt.Fprintln(output, "Created initial commit")

	if opts.github == "" {
		return nil
	}
	if _, err := runner.LookPath("gh"); err != nil {
		fmt.Fprintln(errorOutput, "Warning: GitHub repository was not created because gh was not found.")
		fmt.Fprintln(errorOutput, "Install GitHub CLI from https://cli.github.com, then run gh repo create from the project directory.")
		return nil
	}

	fmt.Fprintln(output, "Checking GitHub CLI authentication...")
	err := run(ghAuthTimeout, "check GitHub CLI authentication", "gh", "auth", "status", "--hostname", "github.com")
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		fmt.Fprintln(errorOutput, "Warning: GitHub repository was not created because gh is not authenticated.")
		fmt.Fprintln(errorOutput, "Run gh auth login, then run gh repo create from the project directory.")
		return nil
	}

	repository := project.Name
	if opts.githubOwner != "" {
		repository = opts.githubOwner + "/" + repository
	}
	args := []string{"repo", "create", repository, "--" + opts.github, "--source=.", "--remote=origin", "--push"}
	fmt.Fprintf(output, "Creating %s GitHub repository %s and pushing the initial commit...\n", opts.github, repository)
	if err := run(ghCreateTimeout, "create GitHub repository "+repository, "gh", args...); err != nil {
		return err
	}
	fmt.Fprintf(output, "Created %s GitHub repository %s\n", opts.github, repository)
	return nil
}

// projectCommands returns the injected runner when present, otherwise the local
// process runner. It does not cache or mutate the application's configuration.
func (a *application) projectCommands() projectCommandRunner {
	if a.commands != nil {
		return a.commands
	}
	return execProjectCommandRunner{}
}

// runProjectCommand gives one command a deadline bounded by parent and annotates
// execution failures with action. When execution fails after the context expires,
// the context error takes precedence so errors.Is can identify cancellation or a
// deadline. A runner that returns success is treated as successful even if the
// context has concurrently expired; enforcing cancellation belongs to the runner.
func runProjectCommand(parent context.Context, runner projectCommandRunner, timeout time.Duration, action, dir string, stdout, stderr io.Writer, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	if err := runner.Run(ctx, dir, stdout, stderr, name, args...); err != nil {
		switch ctx.Err() {
		case context.Canceled:
			return context.Canceled
		case context.DeadlineExceeded:
			return fmt.Errorf("%s timed out after %s: %w", action, timeout, context.DeadlineExceeded)
		default:
			return fmt.Errorf("%s: %w", action, err)
		}
	}
	return nil
}

// ensureGitMetadataAbsent rejects any .git entry, including files and dangling
// symlinks, rather than following it into template-supplied repository metadata.
// This is a point-in-time check, not protection against concurrent filesystem
// changes or Git configuration inherited from outside destination.
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
