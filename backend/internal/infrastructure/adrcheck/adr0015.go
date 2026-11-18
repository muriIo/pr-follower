package adrcheck

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The sign-in step that must come first, and what it must come before (ADR-0015).
const (
	membershipCheck = "requireAllowedOrgMember"
	allowlistEnvVar = "ALLOWED_GITHUB_ORGS"
)

var (
	authErrorRedirect = regexp.MustCompile(`redirectWithAuthError\([^"\n]*"([a-z_]+)"\)`)
	knownAuthErrors   = regexp.MustCompile(`(?s)KNOWN_AUTH_ERRORS[^=]*=\s*\[(.*?)\]`)
)

// validateADR0015OrgAllowlistedSignIn checks that sign-in asks GitHub about organization membership
// before it stores a user or opens a session, that every reason the callback sends the login screen
// is one the SPA knows and translates in both locales, and that the allowlist is documented.
func validateADR0015OrgAllowlistedSignIn(repoRoot string, report *ValidationReport) {
	validateADR0015MembershipFirst(repoRoot, report)
	validateADR0015AuthErrors(repoRoot, report)
	validateADR0015Documented(repoRoot, report)
}

func validateADR0015MembershipFirst(repoRoot string, report *ValidationReport) {
	dir := filepath.Join(repoRoot, "backend", "internal", "usecase", "auth")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "CompleteLogin" || fn.Body == nil {
				continue
			}
			checkAt := token.NoPos
			type store struct {
				label string
				pos   token.Pos
			}
			var stores []store
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if sel.Sel.Name == membershipCheck && checkAt == token.NoPos {
					checkAt = call.Pos()
				}
				if field, ok := sel.X.(*ast.SelectorExpr); ok {
					label := field.Sel.Name + "." + sel.Sel.Name
					if label == "users.Upsert" || label == "sessions.Create" {
						stores = append(stores, store{label, call.Pos()})
					}
				}
				return true
			})
			rel := relPath(repoRoot, path)
			if checkAt == token.NoPos {
				report.Violations = append(report.Violations, fmt.Sprintf(
					"ADR-0015: CompleteLogin in %s never calls %s; check organization membership before storing a user or opening a session", rel, membershipCheck))
				continue
			}
			for _, s := range stores {
				if s.pos < checkAt {
					report.Violations = append(report.Violations, fmt.Sprintf(
						"ADR-0015: CompleteLogin in %s calls %s before %s; a refused account must leave nothing behind", rel, s.label, membershipCheck))
				}
			}
		}
	}
}

func validateADR0015AuthErrors(repoRoot string, report *ValidationReport) {
	sent := map[string]bool{}
	handlerDir := filepath.Join(repoRoot, "backend", "internal", "adapter", "http")
	entries, err := os.ReadDir(handlerDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, readErr := os.ReadFile(filepath.Join(handlerDir, name))
		if readErr != nil {
			continue
		}
		for _, m := range authErrorRedirect.FindAllSubmatch(src, -1) {
			sent[string(m[1])] = true
		}
	}
	if len(sent) == 0 {
		return
	}
	codes := make([]string, 0, len(sent))
	for code := range sent {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	gatewayPath := filepath.Join(repoRoot, "frontend", "src", "app", "infrastructure", "auth", "http-auth.gateway.ts")
	if gateway, err := os.ReadFile(gatewayPath); err == nil {
		known := map[string]bool{}
		if m := knownAuthErrors.FindSubmatch(gateway); m != nil {
			for _, q := range quotedCode.FindAllSubmatch(m[1], -1) {
				known[string(q[1])] = true
			}
		}
		for _, code := range codes {
			if !known[code] {
				report.Violations = append(report.Violations, fmt.Sprintf(
					"ADR-0015: the sign-in callback sends auth_error %q but KNOWN_AUTH_ERRORS in %s does not list it (the login screen would say \"unknown\")",
					code, relPath(repoRoot, gatewayPath)))
			}
		}
	}

	for _, locale := range []string{"pt-BR.json", "en-US.json"} {
		raw, err := os.ReadFile(filepath.Join(repoRoot, "frontend", "public", "i18n", locale))
		if err != nil {
			continue
		}
		var dict map[string]any
		if json.Unmarshal(raw, &dict) != nil {
			continue
		}
		for _, code := range codes {
			if _, ok := dict["auth.error."+code]; !ok {
				report.Violations = append(report.Violations, fmt.Sprintf(
					"ADR-0015: auth_error %q has no %q in %s", code, "auth.error."+code, locale))
			}
		}
	}
}

func validateADR0015Documented(repoRoot string, report *ValidationReport) {
	cfg, err := os.ReadFile(filepath.Join(repoRoot, "backend", "internal", "infrastructure", "config", "config.go"))
	if err != nil || !strings.Contains(string(cfg), allowlistEnvVar) {
		return
	}
	for _, doc := range []string{"docs/production-setup.md", ".env.example"} {
		raw, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(doc)))
		if err != nil || !strings.Contains(string(raw), allowlistEnvVar) {
			report.Violations = append(report.Violations, fmt.Sprintf(
				"ADR-0015: %s does not document %s, which the server reads", doc, allowlistEnvVar))
		}
	}
}
