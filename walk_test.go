package scaner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWalkTreeVisitsFilesAndDirs(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.txt"), []byte("there"), 0o644); err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	err := WalkTree(root, func(path string, d os.DirEntry) error {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		seen[filepath.ToSlash(rel)] = d.IsDir()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !seen["a.txt"] && seen["a.txt"] { // files are !IsDir
		t.Fatalf("unexpected")
	}
	if isDir, ok := seen["a.txt"]; !ok || isDir {
		t.Fatalf("expected file a.txt, got ok=%v isDir=%v", ok, isDir)
	}
	if isDir, ok := seen["sub"]; !ok || !isDir {
		t.Fatalf("expected dir sub, got ok=%v isDir=%v", ok, isDir)
	}
	if isDir, ok := seen["sub/b.txt"]; !ok || isDir {
		t.Fatalf("expected file sub/b.txt, got ok=%v isDir=%v", ok, isDir)
	}
}

func TestWalkTreeSkipDir(t *testing.T) {
	root := t.TempDir()
	skip := filepath.Join(root, "skipme")
	if err := os.Mkdir(skip, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skip, "hidden.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "visible.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}

	seen := map[string]struct{}{}
	err := WalkTree(root, func(path string, d os.DirEntry) error {
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		seen[rel] = struct{}{}
		if d.IsDir() && filepath.Base(path) == "skipme" {
			return SkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := seen["visible.txt"]; !ok {
		t.Fatal("expected visible.txt")
	}
	if _, ok := seen["skipme/hidden.txt"]; ok {
		t.Fatal("did not expect skipme/hidden.txt")
	}
}

func TestDirSizeUsesWalkTree(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := DirSize(root)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("DirSize=%d want 5", n)
	}
}
