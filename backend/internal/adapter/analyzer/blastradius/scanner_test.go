package blastradius_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/adapter/analyzer/blastradius"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

func buildTestTarGz(t *testing.T, files map[string]string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o600,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader(%s): %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%s): %v", name, err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("tw.Close(): %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz.Close(): %v", err)
	}
	return &buf
}

func TestScanner_ScanTarballExtractsImportsAndSkipsNodeModules(t *testing.T) {
	t.Parallel()

	archive := buildTestTarGz(t, map[string]string{
		"arvore-hive-sha123/src/app/api.service.ts": `import { Injectable } from '@angular/core';
import axios from 'axios';
import { retry } from 'axios/retry';

@Injectable()
export class ApiService {}
`,
		"arvore-hive-sha123/src/utils/legacy.js": `const pkg = require("axios");
module.exports = pkg;
`,
		"arvore-hive-sha123/node_modules/axios/index.js": `import axios from 'axios';`,
	})

	scanner := blastradius.NewScanner()
	usages, err := scanner.ScanTarball(context.Background(), archive, []string{"axios"})
	if err != nil {
		t.Fatalf("ScanTarball() error: %v", err)
	}

	if len(usages) != 3 {
		t.Fatalf("expected 3 usages (2 in api.service.ts, 1 in legacy.js), got %d: %#v", len(usages), usages)
	}

	if usages[0].FilePath != "src/app/api.service.ts" || usages[0].LineNumber != 2 {
		t.Errorf("unexpected first usage: %#v", usages[0])
	}
}

func TestScanner_RespectsContextCancellation(t *testing.T) {
	t.Parallel()

	archive := buildTestTarGz(t, map[string]string{
		"repo/src/index.ts": `import axios from 'axios';`,
	})

	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(entity.ErrOperationCanceled)

	scanner := blastradius.NewScanner()
	_, err := scanner.ScanTarball(ctx, archive, []string{"axios"})
	if !errors.Is(err, entity.ErrOperationCanceled) {
		t.Fatalf("expected ErrOperationCanceled, got %v", err)
	}
}

// The archive's order is GitHub's, not the reader's: usages come back sorted by file, then line, so
// the drawer lists them the same way on every analysis.
func TestScanner_ListsUsagesByFileThenLineWhateverTheArchiveOrder(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct{ name, content string }{ // deliberately not in path order
		{"repo-sha/src/z.ts", "import a from 'axios';\n"},
		{"repo-sha/src/a.ts", "const x = 1;\nimport b from 'axios';\nimport c from 'axios/retry';\n"},
		{"repo-sha/lib/m.js", "const pkg = require('axios');\n"},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o600, Size: int64(len(f.content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	usages, err := blastradius.NewScanner().ScanTarball(context.Background(), &buf, []string{"axios"})
	if err != nil {
		t.Fatalf("ScanTarball() error: %v", err)
	}
	got := make([]string, 0, len(usages))
	for _, u := range usages {
		got = append(got, fmt.Sprintf("%s:%d", u.FilePath, u.LineNumber))
	}
	want := []string{"lib/m.js:1", "src/a.ts:2", "src/a.ts:3", "src/z.ts:1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("usages = %v, want %v", got, want)
	}
}
