package entity_test

import (
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

func TestRepository_HasAccess(t *testing.T) {
	t.Parallel()
	cases := map[entity.RepositoryAccess]bool{
		"":                               true, // rows created before access tracking
		entity.RepositoryAccessActive:    true,
		entity.RepositoryAccessRemoved:   false,
		entity.RepositoryAccessSuspended: false,
	}
	for access, want := range cases {
		if got := (entity.Repository{Access: access}).HasAccess(); got != want {
			t.Errorf("Access %q: HasAccess() = %v, want %v", access, got, want)
		}
	}
}

func TestRepository_ApplyAccess(t *testing.T) {
	t.Parallel()

	repo := entity.Repository{InstallationID: 10, Access: entity.RepositoryAccessActive}
	if repo.ApplyAccess(entity.RepositoryAccessActive, 0) {
		t.Fatal("same access and installation is not a change")
	}
	if !repo.ApplyAccess(entity.RepositoryAccessRemoved, 0) || repo.Access != entity.RepositoryAccessRemoved || repo.InstallationID != 10 {
		t.Fatalf("removal must change access and keep the installation: %+v", repo)
	}
	if !repo.ApplyAccess(entity.RepositoryAccessActive, 20) || repo.Access != entity.RepositoryAccessActive || repo.InstallationID != 20 {
		t.Fatalf("re-install must restore access under the new installation: %+v", repo)
	}
	if !repo.ApplyAccess(entity.RepositoryAccessActive, 30) || repo.InstallationID != 30 {
		t.Fatalf("moving to another installation is a change: %+v", repo)
	}

	legacy := entity.Repository{InstallationID: 5}
	if legacy.ApplyAccess(entity.RepositoryAccessActive, 0) {
		t.Fatal("legacy empty access already counts as active")
	}
}
