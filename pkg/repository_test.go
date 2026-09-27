package gogogo

import "testing"

func TestParseRepository(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		value  string
		wanted Repository
	}{
		{
			name:  "short form",
			value: "bcomnes/go-template",
			wanted: Repository{
				User:   "bcomnes",
				Repo:   "go-template",
				Branch: "master",
			},
		},
		{
			name:  "branch",
			value: "bcomnes/go-template#next",
			wanted: Repository{
				User:   "bcomnes",
				Repo:   "go-template",
				Branch: "next",
			},
		},
		{
			name:  "HTTPS URL",
			value: "https://github.com/bcomnes/go-template.git#main",
			wanted: Repository{
				User:   "bcomnes",
				Repo:   "go-template",
				Branch: "main",
			},
		},
		{
			name:  "SSH URL",
			value: "git@github.com:bcomnes/go-template.git#feature/templates",
			wanted: Repository{
				User:   "bcomnes",
				Repo:   "go-template",
				Branch: "feature/templates",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			actual, err := ParseRepository(test.value)
			if err != nil {
				t.Fatalf("ParseRepository() error = %v", err)
			}
			if actual != test.wanted {
				t.Fatalf("ParseRepository() = %#v, want %#v", actual, test.wanted)
			}
			roundTrip, err := ParseRepository(actual.String())
			if err != nil || roundTrip != actual {
				t.Fatalf("canonical round trip = %#v, %v; want %#v", roundTrip, err, actual)
			}
		})
	}
}

func TestParseRepositoryRejectsMalformedValues(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "repository", "owner/repo name", "owner/repo#bad\nbranch", "../repo"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseRepository(value); err == nil {
				t.Fatalf("ParseRepository(%q) unexpectedly succeeded", value)
			}
		})
	}
}

func TestParseRepositoryRejectsRepeatedGitSuffixes(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"owner/repo.git.git#main",
		"owner/repo.git.git.git",
		"https://github.com/owner/repo.git.git#main",
		"https://github.com/owner/repo.git.git/#main",
		"git@github.com:owner/repo.git.git#main",
	} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseRepository(value); err == nil {
				t.Fatalf("ParseRepository(%q) unexpectedly succeeded", value)
			}
		})
	}
}

func TestRepositoryValidate(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		repo  Repository
		valid bool
	}{
		{"valid", Repository{"owner", "repo", "main"}, true},
		{"branch with slash", Repository{"owner", "repo", "feature/templates"}, true},
		{"literal suffix", Repository{"owner", "repo.git", "main"}, true},
		{"empty owner", Repository{"", "repo", "main"}, false},
		{"owner separator", Repository{"owner/extra", "repo", "main"}, false},
		{"owner colon", Repository{"owner:extra", "repo", "main"}, false},
		{"empty repo", Repository{"owner", "", "main"}, false},
		{"repo separator", Repository{"owner", "extra/repo", "main"}, false},
		{"traversal", Repository{"owner", "..", "main"}, false},
		{"empty branch", Repository{"owner", "repo", ""}, false},
		{"control branch", Repository{"owner", "repo", "bad\nbranch"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			before := test.repo
			if err := test.repo.Validate(); (err == nil) != test.valid {
				t.Fatalf("Validate() = %v, want valid = %v", err, test.valid)
			}
			if test.repo != before {
				t.Fatal("Validate mutated repository")
			}
		})
	}
}

func TestRepositoryArchiveURL(t *testing.T) {
	t.Parallel()

	repo := Repository{User: "bcomnes", Repo: "go-template", Branch: "feature/templates"}
	const wanted = "https://github.com/bcomnes/go-template/archive/feature%2Ftemplates.tar.gz"
	if actual := repo.ArchiveURL(); actual != wanted {
		t.Fatalf("ArchiveURL() = %q, want %q", actual, wanted)
	}
}
