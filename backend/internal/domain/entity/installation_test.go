package entity_test

import (
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

func TestInstallationRepository_ToRepository(t *testing.T) {
	t.Parallel()
	src := entity.InstallationRepository{
		GitHubRepoID: 42, InstallationID: 7, Owner: "acme", Name: "web", FullName: "acme/web", DefaultBranch: "trunk", Private: true,
	}
	existing := []entity.CodeownerRule{{Pattern: "*", Owners: []string{"@acme/web"}}}

	repo := src.ToRepository(existing)
	if repo.GitHubRepoID != 42 || repo.InstallationID != 7 || repo.Owner != "acme" || repo.Name != "web" ||
		repo.FullName != "acme/web" || repo.DefaultBranch != "trunk" || len(repo.CachedCodeownersRules) != 1 {
		t.Fatalf("unexpected repository: %#v", repo)
	}

	noBranch := entity.InstallationRepository{GitHubRepoID: 1, Owner: "a", Name: "b", FullName: "a/b"}
	if got := noBranch.ToRepository(nil); got.DefaultBranch != "main" {
		t.Fatalf("expected default branch fallback to main, got %q", got.DefaultBranch)
	}
}
