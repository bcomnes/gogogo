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
		})
	}
}

func TestParseRepositoryRejectsMalformedValues(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "repository", "owner/repo name", "owner/repo#bad\nbranch", "../repo"} {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseRepository(value); err == nil {
				t.Fatalf("ParseRepository(%q) unexpectedly succeeded", value)
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
