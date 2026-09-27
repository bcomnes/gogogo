package gogogo

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// DefaultRepository is the GitHub template used when no repository is configured.
const DefaultRepository = "bcomnes/go-template#master"

// repositoryPartPattern limits owner and repository components to a conservative
// ASCII alphabet. Dot-only traversal components are rejected separately.
var repositoryPartPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// Repository identifies a GitHub repository and branch.
// ParseRepository validates input; callers constructing structs directly should use Validate.
// The value carries no credentials or assurance that the repository exists.
type Repository struct {
	// User is the repository owner's account or organization name.
	User string `json:"user"`
	// Repo is the repository name; ParseRepository removes a Git URL's .git suffix.
	Repo string `json:"repo"`
	// Branch is the archive reference, which may include slashes.
	Branch string `json:"branch"`
}

// ParseRepository parses user/repo, Git URL, and optional #branch forms.
//
// An absent or empty fragment defaults to master, not the remote's default branch.
// Surrounding whitespace is trimmed, a trailing .git suffix is removed, and the
// last two path components supply the owner and repository.
// Repeated trailing .git suffixes are rejected because reparsing the canonical
// reference would otherwise change the repository identity.
// URL hosts, schemes, credentials, and preceding path components are not retained
// or authenticated;
// accepting a URL does not mean subsequent requests will use that URL's host.
// ArchiveURL always targets GitHub.
//
// Owner and repository components must use ASCII letters, digits, underscores,
// dots, or hyphens and cannot equal "." or "..". Branches must be nonempty and contain no
// control characters, but are not checked against Git's full ref-name rules.
// Parsing performs no network requests.
func ParseRepository(value string) (Repository, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Repository{}, fmt.Errorf("repository is required")
	}

	repositoryValue, branch, hasBranch := strings.Cut(value, "#")
	repositoryValue = strings.TrimSpace(repositoryValue)
	if !hasBranch || branch == "" {
		branch = "master"
	}
	branch = strings.TrimSpace(branch)

	if parsed, err := url.Parse(repositoryValue); err == nil && parsed.Scheme != "" && parsed.Path != "" {
		repositoryValue = parsed.Path
	} else if colon := strings.LastIndex(repositoryValue, ":"); colon >= 0 && strings.Contains(repositoryValue[:colon], "@") {
		repositoryValue = repositoryValue[colon+1:]
	}

	repositoryValue = strings.TrimRight(repositoryValue, "/")
	repositoryValue = strings.TrimSuffix(repositoryValue, ".git")
	parts := strings.FieldsFunc(repositoryValue, func(r rune) bool {
		return r == '/' || r == ':'
	})
	if len(parts) < 2 {
		return Repository{}, fmt.Errorf("repository %q is malformed (expected user/repo[#branch])", value)
	}

	repo := Repository{
		User:   parts[len(parts)-2],
		Repo:   parts[len(parts)-1],
		Branch: branch,
	}
	if strings.HasSuffix(repo.Repo, ".git") {
		return Repository{}, fmt.Errorf("repository %q has ambiguous trailing .git suffixes", value)
	}
	if err := repo.Validate(); err != nil {
		return Repository{}, err
	}

	return repo, nil
}

// Validate checks the actual structured fields without parsing or normalizing them.
// Owner and repository must be nonempty ASCII components containing only letters,
// digits, underscores, dots, or hyphens, and cannot be "." or "..".
// Branch must be nonempty and contain no control characters; Git's full ref-name
// rules and repository existence are not checked.
// Unlike ParseRepository, Validate does not supply a default branch or strip a
// .git suffix from a literal repository name.
func (r Repository) Validate() error {
	if !validRepositoryPart(r.User) {
		return fmt.Errorf("repository owner %q is invalid", r.User)
	}
	if !validRepositoryPart(r.Repo) {
		return fmt.Errorf("repository name %q is invalid", r.Repo)
	}
	if r.Branch == "" || strings.IndexFunc(r.Branch, unicode.IsControl) >= 0 {
		return fmt.Errorf("repository branch %q is invalid", r.Branch)
	}
	return nil
}

// validRepositoryPart rejects empty or traversal-only path components without
// claiming to enforce all of GitHub's account and repository naming policies.
func validRepositoryPart(value string) bool {
	return value != "." && value != ".." && repositoryPartPattern.MatchString(value)
}

// String returns the canonical user/repo#branch representation.
// It neither escapes nor validates fields, including fields supplied directly by
// callers rather than through ParseRepository.
func (r Repository) String() string {
	return r.User + "/" + r.Repo + "#" + r.Branch
}

// ArchiveURL returns the GitHub tarball URL for the repository branch.
// Each field is path-escaped independently, so slashes in Branch remain part of
// the reference rather than introducing URL path segments. The method performs no
// validation, authentication, or network access.
func (r Repository) ArchiveURL() string {
	return "https://github.com/" + url.PathEscape(r.User) + "/" + url.PathEscape(r.Repo) + "/archive/" + url.PathEscape(r.Branch) + ".tar.gz"
}
