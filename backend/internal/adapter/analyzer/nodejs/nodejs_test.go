package nodejs_test

import (
	"context"
	"errors"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/adapter/analyzer/nodejs"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
)

func TestAnalyzer_PackageLockV3_TransitivePathAndScope(t *testing.T) {
	t.Parallel()

	analyzer := nodejs.NewAnalyzer()
	if !analyzer.CanAnalyze("package-lock.json") || !analyzer.CanAnalyze("pnpm-lock.yaml") || !analyzer.CanAnalyze("yarn.lock") {
		t.Fatal("expected analyzer to support package-lock.json, pnpm-lock.yaml, and yarn.lock")
	}

	pkgJSON := []byte(`{
		"name": "hive-web",
		"dependencies": {
			"@angular/core": "^22.0.0"
		},
		"devDependencies": {
			"vite": "^6.0.0"
		}
	}`)

	lockJSON := []byte(`{
		"name": "hive-web",
		"lockfileVersion": 3,
		"packages": {
			"": {
				"name": "hive-web",
				"dependencies": { "@angular/core": "^22.0.0" },
				"devDependencies": { "vite": "^6.0.0" }
			},
			"node_modules/vite": {
				"version": "6.0.1",
				"dev": true,
				"dependencies": {
					"postcss": "^8.4.0"
				}
			},
			"node_modules/postcss": {
				"version": "8.4.31",
				"dev": true,
				"dependencies": {
					"nanoid": "^3.3.7"
				}
			},
			"node_modules/nanoid": {
				"version": "3.3.8",
				"dev": true
			}
		}
	}`)

	res, err := analyzer.AnalyzeLockfile(context.Background(), port.DependencyAnalysisInput{
		PullRequest:  entity.PullRequest{ID: 10, HeadSHA: "sha-nanoid"},
		Repository:   entity.Repository{ID: 1, Name: "hive-web"},
		PackageName:  "nanoid",
		FromVersion:  "3.3.6",
		ToVersion:    "3.3.8",
		PackageJSON:  pkgJSON,
		LockfileName: "package-lock.json",
		LockfileRaw:  lockJSON,
	})
	if err != nil {
		t.Fatalf("AnalyzeLockfile() error: %v", err)
	}

	if res.IsDirectDep {
		t.Error("expected nanoid to be transitive (IsDirectDep = false)")
	}
	if res.DepScope != entity.DependencyScopeTransitive {
		t.Errorf("expected scope TRANSITIVE, got %q", res.DepScope)
	}
	if res.SemverBumpType != entity.SemverBumpPatch {
		t.Errorf("expected semver bump PATCH, got %q", res.SemverBumpType)
	}

	// Shortest dependency path: hive-web -> vite -> postcss -> nanoid
	if len(res.DependencyPathGraph) != 4 {
		t.Fatalf("expected 4 nodes in dependency path graph, got %d: %#v", len(res.DependencyPathGraph), res.DependencyPathGraph)
	}
	if res.DependencyPathGraph[0].Name != "hive-web" ||
		res.DependencyPathGraph[1].Name != "vite" ||
		res.DependencyPathGraph[2].Name != "postcss" ||
		res.DependencyPathGraph[3].Name != "nanoid" {
		t.Errorf("unexpected dependency path: %#v", res.DependencyPathGraph)
	}
}

func TestAnalyzer_DirectProdAndCancellation(t *testing.T) {
	t.Parallel()

	analyzer := nodejs.NewAnalyzer()
	pkgJSON := []byte(`{
		"name": "api-gateway",
		"dependencies": {
			"axios": "^1.7.0"
		}
	}`)
	yarnLock := []byte(`
# yarn lockfile v1
axios@^1.7.0:
  version "2.0.0"
  dependencies:
    follow-redirects "^1.15.0"
`)

	res, err := analyzer.AnalyzeLockfile(context.Background(), port.DependencyAnalysisInput{
		PullRequest:  entity.PullRequest{ID: 11, HeadSHA: "sha-axios"},
		Repository:   entity.Repository{ID: 2, Name: "api-gateway"},
		PackageName:  "axios",
		FromVersion:  "1.7.0",
		ToVersion:    "2.0.0",
		PackageJSON:  pkgJSON,
		LockfileName: "yarn.lock",
		LockfileRaw:  yarnLock,
	})
	if err != nil {
		t.Fatalf("AnalyzeLockfile() error: %v", err)
	}

	if !res.IsDirectDep || res.DepScope != entity.DependencyScopeProd {
		t.Errorf("expected direct PROD dependency, got IsDirect=%v Scope=%q", res.IsDirectDep, res.DepScope)
	}
	if res.SemverBumpType != entity.SemverBumpMajor {
		t.Errorf("expected MAJOR bump, got %q", res.SemverBumpType)
	}

	canceledCtx, cancel := context.WithCancelCause(context.Background())
	cancel(entity.ErrOperationCanceled)
	if _, err := analyzer.AnalyzeLockfile(canceledCtx, port.DependencyAnalysisInput{}); !errors.Is(err, entity.ErrOperationCanceled) {
		t.Fatalf("expected ErrOperationCanceled, got %v", err)
	}
}
