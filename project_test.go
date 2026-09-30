package scaner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindProjects_MaxDepth(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{}`)
	deep := filepath.Join(root, "a", "b", "c")
	mustMkdir(t, deep)
	mustWrite(t, filepath.Join(deep, "package.json"), `{}`)
	mustMkdir(t, filepath.Join(deep, "node_modules"))

	// maxDepth=2: a (1), a/b (2) — a/b/c на глубине 3 не посещаем
	projects, err := FindProjects(root, false, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range projects {
		if p.Root == deep {
			t.Fatalf("при maxdepth=2 не должны находить %s", deep)
		}
	}

	projects, err = FindProjects(root, false, 3)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range projects {
		if p.Root == deep {
			found = true
		}
	}
	if !found {
		t.Fatalf("при maxdepth=3 должны найти %s, получено %+v", deep, projects)
	}
}

func TestFindProjects_NodeSkipsNodeModules(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{}`)
	mustMkdir(t, filepath.Join(root, "node_modules", "leftpad"))
	mustWrite(t, filepath.Join(root, "node_modules", "leftpad", "package.json"), `{"name":"leftpad"}`)
	mustWrite(t, filepath.Join(root, "src", "index.js"), `console.log(1)`)

	projects, err := FindProjects(root, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("ожидался 1 проект, получено %d: %+v", len(projects), projects)
	}
	p := projects[0]
	if !hasKind(p, ProjectNode) {
		t.Fatalf("ожидался kind node, получено %v", p.Kinds)
	}
	nm := filepath.Join(root, "node_modules")
	if !hasArtifact(p, nm) {
		t.Fatalf("ожидался артефакт %s, получено %v", nm, p.Artifacts)
	}
	// Внутри node_modules не должно быть отдельного «проекта»
	for _, proj := range projects {
		if proj.Root == filepath.Join(root, "node_modules", "leftpad") {
			t.Fatal("не должны заходить в node_modules и находить вложенный проект")
		}
	}
}

func TestFindProjects_Monorepo(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{}`)
	mustMkdir(t, filepath.Join(root, "node_modules"))
	app := filepath.Join(root, "packages", "app")
	mustMkdir(t, app)
	mustWrite(t, filepath.Join(app, "package.json"), `{}`)
	mustMkdir(t, filepath.Join(app, "node_modules"))

	projects, err := FindProjects(root, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 {
		t.Fatalf("ожидалось 2 проекта, получено %d: %+v", len(projects), projects)
	}
	roots := map[string]bool{}
	for _, p := range projects {
		roots[p.Root] = true
		if !hasArtifact(p, filepath.Join(p.Root, "node_modules")) {
			t.Fatalf("у %s нет node_modules в артефактах: %v", p.Root, p.Artifacts)
		}
	}
	if !roots[root] || !roots[app] {
		t.Fatalf("ожидались корни %s и %s, получено %v", root, app, roots)
	}
}

func TestFindProjects_GoAndNode(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example\n")
	mustWrite(t, filepath.Join(root, "package.json"), `{}`)
	mustMkdir(t, filepath.Join(root, "vendor"))
	mustMkdir(t, filepath.Join(root, "node_modules"))

	projects, err := FindProjects(root, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("ожидался 1 проект, получено %d", len(projects))
	}
	p := projects[0]
	if !hasKind(p, ProjectGo) || !hasKind(p, ProjectNode) {
		t.Fatalf("ожидались go и node, получено %v", p.Kinds)
	}
	if !hasArtifact(p, filepath.Join(root, "vendor")) {
		t.Fatalf("нет vendor: %v", p.Artifacts)
	}
	if !hasArtifact(p, filepath.Join(root, "node_modules")) {
		t.Fatalf("нет node_modules: %v", p.Artifacts)
	}
}

func TestFindProjects_TargetWithoutMarkerIgnored(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "target"))
	mustWrite(t, filepath.Join(root, "readme.txt"), "hi")

	projects, err := FindProjects(root, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("проектов быть не должно, получено %+v", projects)
	}
}

func TestFindProjects_RustTarget(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = \"x\"\n")
	mustMkdir(t, filepath.Join(root, "target"))

	projects, err := FindProjects(root, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || !hasKind(projects[0], ProjectRust) {
		t.Fatalf("ожидался rust-проект: %+v", projects)
	}
	if !hasArtifact(projects[0], filepath.Join(root, "target")) {
		t.Fatalf("нет target: %v", projects[0].Artifacts)
	}
}

func TestCleanForceRemovesOnlyArtifacts(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{}`)
	mustWrite(t, filepath.Join(root, "src", "app.js"), "x")
	nm := filepath.Join(root, "node_modules", "pkg")
	mustMkdir(t, nm)
	mustWrite(t, filepath.Join(nm, "index.js"), "y")

	projects, err := FindProjects(root, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || len(projects[0].Artifacts) == 0 {
		t.Fatalf("ожидались артефакты: %+v", projects)
	}

	// dry-run: ничего не удаляем
	if _, err := os.Stat(filepath.Join(root, "node_modules")); err != nil {
		t.Fatal("node_modules должен существовать до удаления")
	}

	for _, art := range projects[0].Artifacts {
		if err := os.RemoveAll(art); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := os.Stat(filepath.Join(root, "node_modules")); !os.IsNotExist(err) {
		t.Fatal("node_modules должен быть удалён")
	}
	if _, err := os.Stat(filepath.Join(root, "src", "app.js")); err != nil {
		t.Fatal("исходники не должны удаляться")
	}
	if _, err := os.Stat(filepath.Join(root, "package.json")); err != nil {
		t.Fatal("package.json не должен удаляться")
	}
}

func TestFindProjects_GitRepoOnly(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "legacy")
	mustMkdir(t, repo)
	mustMkdir(t, filepath.Join(repo, ".git", "objects"))
	mustWrite(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	mustWrite(t, filepath.Join(repo, ".git", "objects", "pack"), strings.Repeat("x", 100))
	mustWrite(t, filepath.Join(repo, "readme.md"), "hi")

	projects, err := FindProjects(root, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("ожидался 1 git-проект, got %+v", projects)
	}
	if !hasKind(projects[0], ProjectGit) || projects[0].Root != repo {
		t.Fatalf("unexpected project: %+v", projects[0])
	}
}

func TestFindProjects_GoProjectWithGitNotDuplicated(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module x\n")
	mustMkdir(t, filepath.Join(root, ".git"))

	projects, err := FindProjects(root, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || !hasKind(projects[0], ProjectGo) {
		t.Fatalf("ожидался go-проект, got %+v", projects)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func hasKind(p Project, k ProjectKind) bool {
	for _, kind := range p.Kinds {
		if kind == k {
			return true
		}
	}
	return false
}

func hasArtifact(p Project, path string) bool {
	for _, a := range p.Artifacts {
		if a == path {
			return true
		}
	}
	return false
}
