package scaner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFindDSStore(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(root, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}

	rootDS := filepath.Join(root, ".DS_Store")
	nestedDS := filepath.Join(nested, ".DS_Store")
	gitDS := filepath.Join(gitDir, ".DS_Store")
	other := filepath.Join(root, "readme.txt")
	mustWriteFile(t, rootDS, "root")
	mustWriteFile(t, nestedDS, "nested-ds")
	mustWriteFile(t, gitDS, "git")
	mustWriteFile(t, other, "no")

	files, err := FindDSStore(context.Background(), root, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := dsstorePaths(files)
	if !got[rootDS] {
		t.Fatalf("ожидался %s, получено %v", rootDS, dsstoreKeys(got))
	}
	if !got[nestedDS] {
		t.Fatalf("ожидался %s, получено %v", nestedDS, dsstoreKeys(got))
	}
	if got[gitDS] {
		t.Fatal("не должны заходить в .git")
	}
	if got[other] {
		t.Fatal("не должны подхватывать другие файлы")
	}
	if len(got) != 2 {
		t.Fatalf("ожидалось 2 файла, получено %d: %v", len(got), dsstoreKeys(got))
	}
}

func TestFindDSStore_MaxDepth(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	rootDS := filepath.Join(root, ".DS_Store")
	midDS := filepath.Join(root, "a", ".DS_Store")
	deepDS := filepath.Join(deep, ".DS_Store")
	mustWriteFile(t, rootDS, "1")
	mustWriteFile(t, midDS, "2")
	mustWriteFile(t, deepDS, "3")

	files, err := FindDSStore(context.Background(), root, 1)
	if err != nil {
		t.Fatal(err)
	}
	got := dsstorePaths(files)
	if !got[rootDS] {
		t.Fatalf("при maxdepth=1 должен быть %s", rootDS)
	}
	if got[midDS] || got[deepDS] {
		t.Fatalf("при maxdepth=1 не должны находить вложенные, получено %v", dsstoreKeys(got))
	}

	files, err = FindDSStore(context.Background(), root, 2)
	if err != nil {
		t.Fatal(err)
	}
	got = dsstorePaths(files)
	if !got[rootDS] || !got[midDS] {
		t.Fatalf("при maxdepth=2 ожидались %s и %s, получено %v", rootDS, midDS, dsstoreKeys(got))
	}
	if got[deepDS] {
		t.Fatalf("при maxdepth=2 не должны находить %s", deepDS)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func dsstorePaths(files []DSStoreFile) map[string]bool {
	m := make(map[string]bool, len(files))
	for _, f := range files {
		m[f.Path] = true
	}
	return m
}

func dsstoreKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestFindDSStore_Canceled(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".DS_Store"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	files, err := FindDSStore(ctx, root, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ожидался context.Canceled, получено files=%v err=%v", files, err)
	}
}
