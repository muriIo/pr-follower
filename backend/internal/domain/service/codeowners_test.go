package service_test

import (
	"reflect"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/service"
)

func TestParseCodeowners(t *testing.T) {
	t.Parallel()

	raw := `
# Global fallback
* @arvore/core-architects

# Frontend apps
/apps/web/ @murilo @arvore/frontend
*.scss @arvore/design-system
`
	rules := service.ParseCodeowners(raw)
	want := []entity.CodeownerRule{
		{Pattern: "*", Owners: []string{"@arvore/core-architects"}},
		{Pattern: "/apps/web/", Owners: []string{"@murilo", "@arvore/frontend"}},
		{Pattern: "*.scss", Owners: []string{"@arvore/design-system"}},
	}

	if !reflect.DeepEqual(rules, want) {
		t.Fatalf("ParseCodeowners() = %#v, want %#v", rules, want)
	}
}

func TestFilterOwnedChangedFiles_LastMatchingRuleWins(t *testing.T) {
	t.Parallel()

	rules := service.ParseCodeowners(`
* @arvore/platform
/apps/checkout/ @arvore/checkout-team @murilo
/apps/checkout/legacy/ @someone-else
*.sql @arvore/dba
`)

	changed := []entity.ChangedFile{
		{Path: "README.md", Additions: 5, Patch: "+docs"},
		{Path: "apps/checkout/cart.ts", Additions: 20, Patch: "+export class Cart {}"},
		{Path: "apps/checkout/legacy/old.ts", Additions: 2, Patch: "-old"},
		{Path: "db/migrations/001.sql", Additions: 12, Patch: "CREATE TABLE..."},
	}

	user := entity.User{
		Login: "murilo",
		Teams: []string{"@arvore/dba"},
	}

	match := service.EvaluateCodeownersForUser(rules, changed, user)

	if len(match.OwnedFiles) != 2 {
		t.Fatalf("expected 2 owned files, got %d: %#v", len(match.OwnedFiles), match.OwnedFiles)
	}
	if match.OwnedFiles[0].Path != "apps/checkout/cart.ts" {
		t.Errorf("expected first owned file apps/checkout/cart.ts, got %s", match.OwnedFiles[0].Path)
	}
	if match.OwnedFiles[1].Path != "db/migrations/001.sql" {
		t.Errorf("expected second owned file db/migrations/001.sql, got %s", match.OwnedFiles[1].Path)
	}

	if !match.IsTeammateOwner("arvore/checkout-team") && !match.HasCoOwner("@arvore/checkout-team") {
		t.Errorf("expected @arvore/checkout-team to be recognized as co-owner group, got %#v", match.CoOwners)
	}
}
