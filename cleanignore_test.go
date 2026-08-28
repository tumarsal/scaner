package scaner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanIgnore_RootPatterns(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".cleanignore"), "secret/\nvendor/\n")
	mustWrite(t, filepath.Join(root, "app", "package.json"), `{}`)
	mustMkdir(t, filepath.Join(root, "app", "node_modules"))
	mustWrite(t, filepath.Join(root, "secret", "package.json"), `{}`)
	mustMkdir(t, filepath.Join(root, "secret", "node_modules"))

	projects, err := FindProjects(root, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("ожидался 1 проект (app), получено %d: %+v", len(projects), projects)
	}
	if projects[0].Root != filepath.Join(root, "app") {
		t.Fatalf("ожидался app, получено %s", projects[0].Root)
	}
}

func TestCleanIgnore_NestedFile(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "packages", ".cleanignore"), "legacy/\n")
	mustWrite(t, filepath.Join(root, "packages", "app", "package.json"), `{}`)
	mustMkdir(t, filepath.Join(root, "packages", "app", "node_modules"))
	mustWrite(t, filepath.Join(root, "packages", "legacy", "package.json"), `{}`)
	mustMkdir(t, filepath.Join(root, "packages", "legacy", "node_modules"))

	projects, err := FindProjects(root, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("ожидался 1 проект, получено %d: %+v", len(projects), projects)
	}
	if projects[0].Root != filepath.Join(root, "packages", "app") {
		t.Fatalf("ожидался packages/app, получено %s", projects[0].Root)
	}
}

func TestCleanIgnore_Negation(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".cleanignore"), "skip/*\n!skip/keep/\n")
	mustWrite(t, filepath.Join(root, "skip", "keep", "package.json"), `{}`)
	mustMkdir(t, filepath.Join(root, "skip", "keep", "node_modules"))
	mustWrite(t, filepath.Join(root, "skip", "drop", "package.json"), `{}`)
	mustMkdir(t, filepath.Join(root, "skip", "drop", "node_modules"))

	projects, err := FindProjects(root, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("ожидался 1 проект (skip/keep), получено %d: %+v", len(projects), projects)
	}
	if projects[0].Root != filepath.Join(root, "skip", "keep") {
		t.Fatalf("ожидался skip/keep, получено %s", projects[0].Root)
	}
}

func TestCleanIgnore_DirOnlySuffix(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".cleanignore"), "build/\n")
	mustWrite(t, filepath.Join(root, "build", "package.json"), `{}`)
	mustMkdir(t, filepath.Join(root, "build", "node_modules"))

	projects, err := FindProjects(root, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("build/ должен быть исключён, получено %+v", projects)
	}
}

func TestCleanIgnore_MatchUnit(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".cleanignore"), "*.log\n!keep.log\n")

	ci := NewCleanIgnore(root)
	ci.LoadDir(root)
	if ci.byDir[ci.root] == nil {
		t.Fatal("rules not loaded")
	}
	if len(ci.byDir[ci.root].patterns) != 2 {
		t.Fatalf("expected 2 patterns, got %d", len(ci.byDir[ci.root].patterns))
	}

	aLog := filepath.Join(root, "a.log")
	if !ci.Match(aLog, false) {
		t.Fatalf("a.log should be excluded, path=%q root=%q", aLog, ci.root)
	}
	if ci.Match(filepath.Join(root, "keep.log"), false) {
		t.Fatal("keep.log не должен быть исключён (!)")
	}
	if ci.Match(root, true) {
		t.Fatal("корень не исключается")
	}
}

func TestCleanIgnore_GlobLegacy(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".cleanignore"), "**/legacy\n")
	mustWrite(t, filepath.Join(root, "packages", "legacy", "package.json"), `{}`)

	projects, err := FindProjects(root, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("**/legacy должен исключить проект, получено %+v", projects)
	}
}

func TestCleanIgnore_FileExists(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, cleanIgnoreFilename), []byte("# comment\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ci := NewCleanIgnore(root)
	ci.LoadDir(root)
	if ci.byDir[root] == nil {
		t.Fatal("ожидались правила из .cleanignore")
	}
}
