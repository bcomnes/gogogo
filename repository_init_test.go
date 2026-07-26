package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	gogogo "github.com/bcomnes/gogogo/pkg"
)

func TestInitializeRepositoryCreatesGitHubRepository(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		owner      string
		visibility string
		repository string
	}{
		{name: "authenticated user", visibility: "private", repository: "example"},
		{name: "organization", owner: "acme", visibility: "public", repository: "acme/example"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			destination := t.TempDir()
			commands := localGitCommandSteps()
			commands = append(commands,
				testProjectCommand{name: "gh", args: []string{"auth", "status", "--hostname", "github.com"}},
				testProjectCommand{
					name:   "gh",
					args:   []string{"repo", "create", test.repository, "--" + test.visibility, "--source=.", "--remote=origin", "--push"},
					stdout: "https://github.com/" + test.repository + "\n",
				},
			)
			runner := newTestProjectCommandRunner(t, commands...)
			app := &application{commands: runner}
			var output bytes.Buffer
			opts := options{github: test.visibility, githubOwner: test.owner}

			if err := app.initializeRepository(context.Background(), projectForTest(destination), opts, &output, io.Discard); err != nil {
				t.Fatalf("initializeRepository() error = %v", err)
			}
			runner.done()
			if !strings.Contains(output.String(), "https://github.com/"+test.repository) {
				t.Fatalf("stdout = %q", output.String())
			}
		})
	}
}

func TestInitializeRepositorySkipsUnavailableGitHubCLI(t *testing.T) {
	t.Parallel()

	runner := newTestProjectCommandRunner(t, localGitCommandSteps()...)
	runner.lookPathErr = errors.New("not found")
	app := &application{commands: runner}
	var errorOutput bytes.Buffer

	if err := app.initializeRepository(context.Background(), projectForTest(t.TempDir()), options{github: "private"}, io.Discard, &errorOutput); err != nil {
		t.Fatalf("initializeRepository() error = %v", err)
	}
	runner.done()
	if !strings.Contains(errorOutput.String(), "gh was not found") {
		t.Fatalf("stderr = %q", errorOutput.String())
	}
}

func TestInitializeRepositorySkipsUnauthenticatedGitHubCLI(t *testing.T) {
	t.Parallel()

	commands := localGitCommandSteps()
	commands = append(commands, testProjectCommand{
		name:   "gh",
		args:   []string{"auth", "status", "--hostname", "github.com"},
		stderr: "not logged in\n",
		err:    errors.New("exit 1"),
	})
	runner := newTestProjectCommandRunner(t, commands...)
	app := &application{commands: runner}
	var errorOutput bytes.Buffer

	if err := app.initializeRepository(context.Background(), projectForTest(t.TempDir()), options{github: "private"}, io.Discard, &errorOutput); err != nil {
		t.Fatalf("initializeRepository() error = %v", err)
	}
	runner.done()
	if !strings.Contains(errorOutput.String(), "not logged in") || !strings.Contains(errorOutput.String(), "gh is not authenticated") {
		t.Fatalf("stderr = %q", errorOutput.String())
	}
}

func TestInitializeRepositoryGitFailures(t *testing.T) {
	t.Parallel()

	commandErr := errors.New("command failed")
	tests := []struct {
		name         string
		commands     []testProjectCommand
		wantedAction string
	}{
		{
			name:         "init",
			commands:     []testProjectCommand{{name: "git", args: []string{"init"}, stderr: "init failed\n", err: commandErr}},
			wantedAction: "initialize Git repository",
		},
		{
			name: "add",
			commands: []testProjectCommand{
				{name: "git", args: []string{"init"}},
				{name: "git", args: []string{"add", "--all"}, stderr: "add failed\n", err: commandErr},
			},
			wantedAction: "stage project files",
		},
		{
			name: "commit",
			commands: []testProjectCommand{
				{name: "git", args: []string{"init"}},
				{name: "git", args: []string{"add", "--all"}},
				{name: "git", args: []string{"commit", "--allow-empty", "-m", "Initial commit"}, stderr: "identity unknown\n", err: commandErr},
			},
			wantedAction: "create initial commit",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := newTestProjectCommandRunner(t, test.commands...)
			app := &application{commands: runner}
			var output bytes.Buffer
			var errorOutput bytes.Buffer
			err := app.initializeRepository(context.Background(), projectForTest(t.TempDir()), options{}, &output, &errorOutput)

			if !errors.Is(err, commandErr) || !strings.Contains(err.Error(), test.wantedAction) {
				t.Fatalf("initializeRepository() error = %v", err)
			}
			runner.done()
			if errorOutput.Len() == 0 {
				t.Fatal("command stderr was not streamed")
			}
		})
	}
}

func TestInitializeRepositoryReturnsGitHubCreationFailure(t *testing.T) {
	t.Parallel()

	commandErr := errors.New("exit 1")
	commands := localGitCommandSteps()
	commands = append(commands,
		testProjectCommand{name: "gh", args: []string{"auth", "status", "--hostname", "github.com"}},
		testProjectCommand{
			name:   "gh",
			args:   []string{"repo", "create", "example", "--private", "--source=.", "--remote=origin", "--push"},
			stderr: "network unavailable\n",
			err:    commandErr,
		},
	)
	runner := newTestProjectCommandRunner(t, commands...)
	app := &application{commands: runner}
	var output bytes.Buffer
	var errorOutput bytes.Buffer

	err := app.initializeRepository(context.Background(), projectForTest(t.TempDir()), options{github: "private"}, &output, &errorOutput)
	if !errors.Is(err, commandErr) || !strings.Contains(err.Error(), "create GitHub repository example") {
		t.Fatalf("initializeRepository() error = %v", err)
	}
	runner.done()
	if !strings.Contains(errorOutput.String(), "network unavailable") {
		t.Fatalf("stderr = %q", errorOutput.String())
	}
	if strings.Contains(output.String(), "Created private GitHub repository") {
		t.Fatalf("stdout contains success message: %q", output.String())
	}
}

func TestRunProjectCommandContextErrors(t *testing.T) {
	t.Parallel()

	t.Run("timeout", func(t *testing.T) {
		err := runProjectCommand(context.Background(), blockingProjectCommandRunner{}, time.Millisecond, "run slow command", t.TempDir(), io.Discard, io.Discard, "slow")
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timed out after") {
			t.Fatalf("runProjectCommand() error = %v", err)
		}
	})

	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := runProjectCommand(ctx, blockingProjectCommandRunner{}, time.Minute, "run slow command", t.TempDir(), io.Discard, io.Discard, "slow")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runProjectCommand() error = %v", err)
		}
	})
}

func TestInitializeRepositoryRejectsTemplateGitMetadata(t *testing.T) {
	t.Parallel()

	destination := t.TempDir()
	if err := os.Mkdir(filepath.Join(destination, ".git"), 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	runner := newTestProjectCommandRunner(t)
	app := &application{commands: runner}
	if err := app.initializeRepository(context.Background(), projectForTest(destination), options{}, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "template contains") {
		t.Fatalf("initializeRepository() error = %v", err)
	}
	runner.done()
}

func TestExecProjectCommandRunnerStreamsOutput(t *testing.T) {
	if os.Getenv("GOGOGO_COMMAND_HELPER") == "1" {
		fmt.Fprintln(os.Stdout, "stdout marker")
		fmt.Fprintln(os.Stderr, "stderr marker")
		time.Sleep(10 * time.Second)
		return
	}

	t.Setenv("GOGOGO_COMMAND_HELPER", "1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	stdoutSeen := make(chan struct{})
	stderrSeen := make(chan struct{})
	done := make(chan error, 1)
	runner := execProjectCommandRunner{}
	go func() {
		done <- runner.Run(
			ctx,
			t.TempDir(),
			&notifyingWriter{destination: &stdout, signal: stdoutSeen},
			&notifyingWriter{destination: &stderr, signal: stderrSeen},
			os.Args[0],
			"-test.run=^TestExecProjectCommandRunnerStreamsOutput$",
		)
	}()

	for name, signal := range map[string]<-chan struct{}{"stdout": stdoutSeen, "stderr": stderrSeen} {
		select {
		case <-signal:
		case <-time.After(2 * time.Second):
			t.Fatalf("did not observe %s before command exit", name)
		}
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("Run() unexpectedly succeeded after cancellation")
	}
	if !strings.Contains(stdout.String(), "stdout marker") || !strings.Contains(stderr.String(), "stderr marker") {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}

type testProjectCommand struct {
	name   string
	args   []string
	stdout string
	stderr string
	err    error
}

type testProjectCommandRunner struct {
	t           *testing.T
	commands    []testProjectCommand
	lookPathErr error
}

func newTestProjectCommandRunner(t *testing.T, commands ...testProjectCommand) *testProjectCommandRunner {
	t.Helper()
	return &testProjectCommandRunner{t: t, commands: append([]testProjectCommand(nil), commands...)}
}

func (runner *testProjectCommandRunner) LookPath(name string) (string, error) {
	runner.t.Helper()
	if name != "gh" {
		runner.t.Fatalf("unexpected executable lookup: %s", name)
	}
	if runner.lookPathErr != nil {
		return "", runner.lookPathErr
	}
	return "/usr/local/bin/gh", nil
}

func (runner *testProjectCommandRunner) Run(_ context.Context, dir string, stdout, stderr io.Writer, name string, args ...string) error {
	runner.t.Helper()
	if len(runner.commands) == 0 {
		runner.t.Fatalf("unexpected command in %s: %s %s", dir, name, strings.Join(args, " "))
	}
	command := runner.commands[0]
	runner.commands = runner.commands[1:]
	if name != command.name || !slices.Equal(args, command.args) {
		runner.t.Fatalf("command mismatch\n got: %s %v\nwant: %s %v", name, args, command.name, command.args)
	}
	_, _ = io.WriteString(stdout, command.stdout)
	_, _ = io.WriteString(stderr, command.stderr)
	return command.err
}

func (runner *testProjectCommandRunner) done() {
	runner.t.Helper()
	if len(runner.commands) != 0 {
		runner.t.Fatalf("%d expected commands were not run; next is %s %v", len(runner.commands), runner.commands[0].name, runner.commands[0].args)
	}
}

type blockingProjectCommandRunner struct{}

func (blockingProjectCommandRunner) LookPath(string) (string, error) {
	return "", nil
}

func (blockingProjectCommandRunner) Run(ctx context.Context, _ string, _, _ io.Writer, _ string, _ ...string) error {
	<-ctx.Done()
	return ctx.Err()
}

type notifyingWriter struct {
	destination io.Writer
	signal      chan<- struct{}
	once        sync.Once
}

func (writer *notifyingWriter) Write(data []byte) (int, error) {
	count, err := writer.destination.Write(data)
	writer.once.Do(func() { close(writer.signal) })
	return count, err
}

func localGitCommandSteps() []testProjectCommand {
	return []testProjectCommand{
		{name: "git", args: []string{"init"}},
		{name: "git", args: []string{"add", "--all"}},
		{name: "git", args: []string{"commit", "--allow-empty", "-m", "Initial commit"}},
	}
}

func projectForTest(destination string) gogogo.Project {
	return gogogo.Project{Name: "example", Destination: destination}
}
