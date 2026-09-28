# Contributing

## Releases

The manual **Release** workflow uses [`bcomnes/go-bump`](https://github.com/bcomnes/go-bump) and the `goversion` tool pinned in `go.mod`.
After the workflow is merged, open **Actions → Release → Run workflow** and select the default branch (`master`).
Other branches are skipped, and release runs are serialized to avoid competing version bumps.

Choose a version directive such as `patch` or `minor`.
For `custom`, also enter an explicit semantic version such as `0.2.0`, without the leading `v`.
The workflow publishes by default.
Check **Dry run — test the release without publishing** to create and validate a local release candidate on the runner without pushing refs, creating a GitHub Release, or seeding the Go proxy.
The workflow tests the current source, then runs race tests, vet, and a build against the exact release commit before publication.

Publication uses the built-in `GITHUB_TOKEN` with job-scoped `contents: write`; no additional secret is required.
Repository rules must allow that token to push the release commit and tag to the default branch.
Events created using this token generally do not trigger further GitHub Actions workflows, so do not rely on tag-push or release events from this workflow to start another workflow.
Publication also seeds the public Go module proxy.

The local release commands remain available:

```console
make version bump=patch
go test -race ./...
go vet ./...
make build
make publish args=-dry
make publish
```

If publication fails after creating a release commit, do not blindly rerun the whole workflow: another relative bump could create the next version.
Inspect the failed run's version, commit, and tag, fetch the default branch and tags, and use `make publish` from the matching clean release checkout to resume publication.
If nothing was pushed, recreate the exact intended version locally with `make version bump=0.2.0` (substituting the version from the failed run), validate it, and publish.
Never move or recreate an already published version tag.
See the [go-bump recovery guide](https://github.com/bcomnes/go-bump#recovering-a-failed-publication) for details.

## Guidelines

- Patches, ideas, and changes are welcome.
- Bug fixes are almost always welcome.
- New features are sometimes welcome:
  - Please open an issue to discuss the idea **before** investing significant time.
  - The proposal may be rejected.
  - If you’d rather skip the discussion and jump straight into implementation, be prepared to maintain a fork if the idea is respectfully declined.
- Please follow the style of the existing code.
- All tests must pass.
- New features or code paths must include tests.
- Aim for 100% test coverage.
- Questions are welcome! However, unless there is an official support contract in place, support is not guaranteed.
- Contributors reserve the right to walk away from the project at any time, with or without notice.
