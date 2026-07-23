package goproject

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// DefaultRepository is the GitHub template used when no repository is configured.
const DefaultRepository = "bcomnes/go-template#master"

var repositoryPartPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// Repository identifies a GitHub repository and branch.
type Repository struct {
	User   string `json:"user"`
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
}

// ParseRepository parses user/repo, Git URL, and optional #branch forms.
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
	if !validRepositoryPart(repo.User) {
		return Repository{}, fmt.Errorf("repository owner %q is invalid", repo.User)
	}
	if !validRepositoryPart(repo.Repo) {
		return Repository{}, fmt.Errorf("repository name %q is invalid", repo.Repo)
	}
	if repo.Branch == "" || strings.IndexFunc(repo.Branch, unicode.IsControl) >= 0 {
		return Repository{}, fmt.Errorf("repository branch %q is invalid", repo.Branch)
	}

	return repo, nil
}

func validRepositoryPart(value string) bool {
	return value != "." && value != ".." && repositoryPartPattern.MatchString(value)
}

// String returns the canonical user/repo#branch representation.
func (r Repository) String() string {
	return r.User + "/" + r.Repo + "#" + r.Branch
}

// ArchiveURL returns the GitHub tarball URL for the repository branch.
func (r Repository) ArchiveURL() string {
	return "https://github.com/" + url.PathEscape(r.User) + "/" + url.PathEscape(r.Repo) + "/archive/" + url.PathEscape(r.Branch) + ".tar.gz"
}
