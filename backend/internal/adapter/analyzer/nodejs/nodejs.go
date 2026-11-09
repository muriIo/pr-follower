package nodejs

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
	"github.com/murilo/pr-follower/backend/internal/domain/service"
)

type Analyzer struct{}

func NewAnalyzer() *Analyzer {
	return &Analyzer{}
}

func (a *Analyzer) Ecosystem() string {
	return "npm"
}

func (a *Analyzer) CanAnalyze(lockfileName string) bool {
	switch lockfileName {
	case "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml":
		return true
	default:
		return false
	}
}

type packageJSONManifest struct {
	Name            string            `json:"name"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

type npmLockV3 struct {
	Name     string                         `json:"name"`
	Packages map[string]npmLockPackageEntry `json:"packages"`
}

type npmLockPackageEntry struct {
	Version         string            `json:"version"`
	Dev             bool              `json:"dev"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func (a *Analyzer) AnalyzeLockfile(ctx context.Context, input port.DependencyAnalysisInput) (*entity.DependabotAnalysis, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}

	var manifest packageJSONManifest
	if len(input.PackageJSON) > 0 {
		_ = json.Unmarshal(input.PackageJSON, &manifest)
	}

	rootName := input.Repository.Name
	if rootName == "" {
		rootName = manifest.Name
	}
	if rootName == "" {
		rootName = "root"
	}

	isDirect := false
	scope := entity.DependencyScopeTransitive
	if _, ok := manifest.Dependencies[input.PackageName]; ok {
		isDirect = true
		scope = entity.DependencyScopeProd
	} else if _, ok := manifest.DevDependencies[input.PackageName]; ok {
		isDirect = true
		scope = entity.DependencyScopeDev
	}

	var graph []entity.DependencyNode
	if strings.HasSuffix(input.LockfileName, ".json") && len(input.LockfileRaw) > 0 {
		graph = buildNpmV3DependencyPath(rootName, input.PackageName, input.ToVersion, scope, manifest, input.LockfileRaw)
	}
	if len(graph) == 0 {
		graph = []entity.DependencyNode{
			{Name: rootName, Version: "root", Scope: entity.DependencyScopeProd},
			{Name: input.PackageName, Version: input.ToVersion, Scope: scope},
		}
	}

	bumpType := service.ClassifySemverBump(input.FromVersion, input.ToVersion)

	return &entity.DependabotAnalysis{
		PullRequestID:       input.PullRequest.ID,
		HeadSHA:             input.PullRequest.HeadSHA,
		Ecosystem:           a.Ecosystem(),
		PackageName:         input.PackageName,
		FromVersion:         input.FromVersion,
		ToVersion:           input.ToVersion,
		SemverBumpType:      bumpType,
		IsDirectDep:         isDirect,
		DepScope:            scope,
		DependencyPathGraph: graph,
		AdvisoriesAndNotes:  input.Advisories,
	}, nil
}

func buildNpmV3DependencyPath(
	rootName, targetPkg, targetVersion string,
	targetScope entity.DependencyScope,
	manifest packageJSONManifest,
	lockRaw []byte,
) []entity.DependencyNode {
	var lock npmLockV3
	if err := json.Unmarshal(lockRaw, &lock); err != nil || len(lock.Packages) == 0 {
		return nil
	}

	adj := make(map[string][]string)
	versions := make(map[string]string)
	scopes := make(map[string]entity.DependencyScope)

	versions[rootName] = "root"
	scopes[rootName] = entity.DependencyScopeProd

	for dep := range manifest.Dependencies {
		adj[rootName] = append(adj[rootName], dep)
		scopes[dep] = entity.DependencyScopeProd
	}
	for dep := range manifest.DevDependencies {
		adj[rootName] = append(adj[rootName], dep)
		if _, exists := scopes[dep]; !exists {
			scopes[dep] = entity.DependencyScopeDev
		}
	}

	for key, entry := range lock.Packages {
		if key == "" {
			for dep := range entry.Dependencies {
				adj[rootName] = appendUnique(adj[rootName], dep)
			}
			for dep := range entry.DevDependencies {
				adj[rootName] = appendUnique(adj[rootName], dep)
			}
			continue
		}
		pkgName := extractNodeModulesPkgName(key)
		if pkgName == "" {
			continue
		}
		if entry.Version != "" {
			versions[pkgName] = entry.Version
		}
		if _, ok := scopes[pkgName]; !ok {
			if entry.Dev {
				scopes[pkgName] = entity.DependencyScopeTransitive
			} else {
				scopes[pkgName] = entity.DependencyScopeProd
			}
		}
		for child := range entry.Dependencies {
			adj[pkgName] = appendUnique(adj[pkgName], child)
		}
	}

	// BFS shortest path from rootName to targetPkg
	queue := [][]string{{rootName}}
	visited := map[string]bool{rootName: true}

	for len(queue) > 0 {
		currPath := queue[0]
		queue = queue[1:]
		last := currPath[len(currPath)-1]

		if last == targetPkg {
			nodes := make([]entity.DependencyNode, 0, len(currPath))
			for _, name := range currPath {
				ver := versions[name]
				if name == targetPkg && targetVersion != "" {
					ver = targetVersion
				}
				sc := scopes[name]
				if name == targetPkg {
					sc = targetScope
				}
				nodes = append(nodes, entity.DependencyNode{
					Name:    name,
					Version: ver,
					Scope:   sc,
				})
			}
			return nodes
		}

		for _, next := range adj[last] {
			if !visited[next] {
				visited[next] = true
				nextPath := make([]string, len(currPath)+1)
				copy(nextPath, currPath)
				nextPath[len(currPath)] = next
				queue = append(queue, nextPath)
			}
		}
	}

	return nil
}

func extractNodeModulesPkgName(key string) string {
	const marker = "node_modules/"
	idx := strings.LastIndex(key, marker)
	if idx == -1 {
		return ""
	}
	return key[idx+len(marker):]
}

func appendUnique(slice []string, item string) []string {
	for _, s := range slice {
		if s == item {
			return slice
		}
	}
	return append(slice, item)
}
