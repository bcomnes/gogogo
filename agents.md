# Agent Guidelines

- Write Markdown prose with one sentence per line so git diffs stay focused and readable.
- Format changed Go files with `gofmt` before finishing.
- Prefer the Go standard library unless a dependency clearly improves the implementation.
- Run `go mod tidy` only when adding, removing, or changing module dependencies.
- Keep `go.mod` and `go.sum` changes intentional and review them before finishing.
- Use the existing `Makefile` targets when they fit the task.
- Prefer `make test` or `go test ./...` for validation after code changes.
- Update exported identifiers' doc comments when changing public API behavior.
- When handling PR review comments, validate that each comment is correct before making changes; maintainer comments are almost always valid, but review bot comments may be wrong, and after addressing a comment, always reply with what was done.
