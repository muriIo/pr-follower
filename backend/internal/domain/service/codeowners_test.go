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

// The pattern rules GitHub documents for CODEOWNERS (gitignore-like, with its own exceptions).
func TestEvaluateCodeowners_FollowsGitHubPatternRules(t *testing.T) {
	t.Parallel()
	me := entity.User{Login: "me"}
	owns := func(pattern, file string) bool {
		rules := []entity.CodeownerRule{{Pattern: pattern, Owners: []string{"@me"}}}
		return len(service.EvaluateCodeownersForUser(rules, []entity.ChangedFile{{Path: file}}, me).OwnedFiles) == 1
	}
	for _, tc := range []struct {
		pattern, file string
		want          bool
	}{
		// "*" owns everything.
		{"*", "README.md", true},
		{"*", "src/deep/nested/file.go", true},
		// "*.js" owns that extension at any depth.
		{"*.js", "index.js", true},
		{"*.js", "src/app/index.js", true},
		{"*.js", "src/app/index.ts", false},
		// "/build/logs/" owns only the root build/logs directory and what is under it.
		{"/build/logs/", "build/logs/a.log", true},
		{"/build/logs/", "build/logs/2026/a.log", true},
		{"/build/logs/", "other/build/logs/a.log", false},
		{"/build/logs/", "build/logs", false},
		// "docs/*" owns files directly in docs, not nested ones.
		{"docs/*", "docs/getting-started.md", true},
		{"docs/*", "docs/build-app/troubleshooting.md", false},
		// "apps/" owns any apps directory, anywhere.
		{"apps/", "apps/web/main.ts", true},
		{"apps/", "packages/apps/x.ts", true},
		{"apps/", "packages/myapps/x.ts", false},
		// "/docs/" owns the root docs directory only.
		{"/docs/", "docs/a.md", true},
		{"/docs/", "docs/deep/a.md", true},
		{"/docs/", "src/docs/a.md", false},
		{"/docs/", "docs-site/a.md", false},
		// "**/logs" owns any logs directory at any depth.
		{"**/logs", "build/logs/a.log", true},
		{"**/logs", "deeply/nested/logs/a.log", true},
		{"**/logs", "logs/a.log", true},
		{"**/logs", "build/logsx/a.log", false},
		// "/apps/github" owns that file or directory at the root.
		{"/apps/github", "apps/github/x.go", true},
		{"/apps/github", "apps/github", true},
		{"/apps/github", "apps/github-enterprise/x.go", false},
		// A path with a slash in the middle is anchored at the root.
		{"src/app/", "src/app/x.ts", true},
		{"src/app/", "lib/src/app/x.ts", false},
		// "**" in the middle and at the end.
		{"/src/**/test/", "src/a/b/test/x.ts", true},
		{"/src/**/test/", "src/test/x.ts", true},
		{"/src/**", "src/a/b.ts", true},
		{"/src/**", "lib/src/a.ts", false},
		// A file name without a slash matches at any depth.
		{"Makefile", "Makefile", true},
		{"Makefile", "tools/Makefile", true},
		{"/Makefile", "tools/Makefile", false},
	} {
		if got := owns(tc.pattern, tc.file); got != tc.want {
			t.Errorf("pattern %q, file %q: owned = %v, want %v", tc.pattern, tc.file, got, tc.want)
		}
	}
}
