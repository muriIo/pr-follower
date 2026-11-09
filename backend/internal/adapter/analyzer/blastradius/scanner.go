package blastradius

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"path"
	"regexp"
	"strings"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

type Scanner struct{}

func NewScanner() *Scanner {
	return &Scanner{}
}

func (s *Scanner) ScanTarball(
	ctx context.Context,
	tarballStream io.Reader,
	targetPackages []string,
) ([]entity.BlastRadiusUsage, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	if len(targetPackages) == 0 {
		return nil, nil
	}

	patterns := compileImportRegexes(targetPackages)
	if len(patterns) == 0 {
		return nil, nil
	}

	gz, err := gzip.NewReader(tarballStream)
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var usages []entity.BlastRadiusUsage

	for {
		if err := ctx.Err(); err != nil {
			return nil, context.Cause(ctx)
		}

		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}

		if hdr.Typeflag == tar.TypeDir {
			continue
		}

		relPath := stripArchiveRootPrefix(hdr.Name)
		if !isScannableSourceFile(relPath) {
			continue
		}

		raw, err := io.ReadAll(io.LimitReader(tr, 2*1024*1024))
		if err != nil {
			continue
		}

		lines := strings.Split(string(raw), "\n")
		for idx, line := range lines {
			for _, re := range patterns {
				if re.MatchString(line) {
					usages = append(usages, entity.BlastRadiusUsage{
						FilePath:    relPath,
						LineNumber:  idx + 1,
						ImportStmt:  strings.TrimSpace(line),
						CodeSnippet: extractSnippet(lines, idx, 2),
					})
					break
				}
			}
		}
	}

	return usages, nil
}

func compileImportRegexes(pkgs []string) []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, pkg := range pkgs {
		clean := strings.TrimSpace(pkg)
		if clean == "" {
			continue
		}
		escaped := regexp.QuoteMeta(clean)
		// Match ES import/export or CommonJS require of `pkg` or `pkg/...`
		expr := `(?:from\s+|import\s*\(\s*|require\s*\(\s*)['"]` + escaped + `(?:['"/])`
		if re, err := regexp.Compile(expr); err == nil {
			out = append(out, re)
		}
	}
	return out
}

func stripArchiveRootPrefix(name string) string {
	trimmed := strings.TrimPrefix(name, "./")
	if idx := strings.IndexByte(trimmed, '/'); idx != -1 {
		return trimmed[idx+1:]
	}
	return trimmed
}

func isScannableSourceFile(filePath string) bool {
	if filePath == "" {
		return false
	}
	for _, ignored := range []string{"node_modules/", "dist/", "vendor/", ".git/", "coverage/"} {
		if strings.HasPrefix(filePath, ignored) || strings.Contains(filePath, "/"+ignored) {
			return false
		}
	}
	ext := strings.ToLower(path.Ext(filePath))
	switch ext {
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		return true
	default:
		return false
	}
}

func extractSnippet(lines []string, targetIdx, radius int) string {
	start := max(0, targetIdx-radius)
	end := min(len(lines)-1, targetIdx+radius)
	return strings.Join(lines[start:end+1], "\n")
}
