package service

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeMinerUResultZIPRejectsUnsafeArchive(t *testing.T) {
	archivePath := writeTestZIP(t, map[string]string{
		"../outside.txt": "must not escape",
		"full.md":        "# full",
	})

	if err := normalizeMinerUResultZIP(context.Background(), archivePath); err == nil {
		t.Fatal("normalizeMinerUResultZIP accepted a path-traversal entry")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(archivePath), "outside.txt")); !os.IsNotExist(err) {
		t.Fatalf("path-traversal entry escaped extraction workspace: %v", err)
	}
}

func TestNormalizeMinerUResultZIPRequiresFullMarkdown(t *testing.T) {
	archivePath := writeTestZIP(t, map[string]string{
		"result-root/content_list.json": "metadata",
		"result-root/images/image.png":  "image",
	})

	err := normalizeMinerUResultZIP(context.Background(), archivePath)
	if err == nil || !strings.Contains(err.Error(), "full.md") {
		t.Fatalf("normalizeMinerUResultZIP error = %v, want missing full.md", err)
	}
}

func TestNormalizeMinerUResultZIPPreservesMarkdownImageBlankLine(t *testing.T) {
	const markdown = "重庆交通大学项目说明。\n\n![](images/slide-1.png)\n"
	archivePath := writeTestZIP(t, map[string]string{
		"result-root/full.md":            markdown,
		"result-root/images/slide-1.png": "image-bytes",
		"result-root/middle.json":        "must be removed",
	})

	if err := normalizeMinerUResultZIP(context.Background(), archivePath); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, entry := range reader.File {
		if entry.Name != "result-root/full.md" {
			continue
		}
		input, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, readErr := io.ReadAll(input)
		closeErr := input.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		if string(content) != markdown {
			t.Fatalf("normalized full.md = %q, want %q", content, markdown)
		}
		return
	}
	t.Fatal("normalized ZIP does not contain full.md")
}

func writeTestZIP(t *testing.T, entries map[string]string) string {
	t.Helper()
	archivePath := filepath.Join(t.TempDir(), "mineru-result.zip")
	output, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(output)
	for name, content := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			_ = output.Close()
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			_ = output.Close()
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		_ = output.Close()
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	return archivePath
}
