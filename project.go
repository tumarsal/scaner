package scaner

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/dustin/go-humanize"
)

// ProjectKind — тип найденного проекта разработки.
type ProjectKind string

const (
	ProjectNode   ProjectKind = "node"
	ProjectGo     ProjectKind = "go"
	ProjectRust   ProjectKind = "rust"
	ProjectPython ProjectKind = "python"
	ProjectJava   ProjectKind = "java"
	ProjectGradle ProjectKind = "gradle"
	ProjectDotnet ProjectKind = "dotnet"
	ProjectPHP    ProjectKind = "php"
	ProjectRuby   ProjectKind = "ruby"
	ProjectElixir ProjectKind = "elixir"
	ProjectCMake  ProjectKind = "cmake"
	ProjectSwift  ProjectKind = "swift"
	ProjectDart   ProjectKind = "dart"
	ProjectGit    ProjectKind = "git"
)

// Project — найденный корень проекта с типами и существующими артефактами.
type Project struct {
	Root      string
	Kinds     []ProjectKind
	Artifacts []string
}

// markerRules — точные имена файлов-маркеров в корне проекта.
var markerRules = []struct {
	name string
	kind ProjectKind
	arts []string
}{
	{"package.json", ProjectNode, []string{"node_modules", "dist", "build", ".next", ".nuxt", ".turbo", "coverage", ".nyc_output"}},
	{"go.mod", ProjectGo, []string{"vendor"}},
	{"Cargo.toml", ProjectRust, []string{"target"}},
	{"pyproject.toml", ProjectPython, []string{".venv", "venv", "__pycache__", ".pytest_cache", ".mypy_cache", "dist", ".tox"}},
	{"requirements.txt", ProjectPython, []string{".venv", "venv", "__pycache__", ".pytest_cache", ".mypy_cache", "dist", ".tox"}},
	{"Pipfile", ProjectPython, []string{".venv", "venv", "__pycache__", ".pytest_cache", ".mypy_cache", "dist", ".tox"}},
	{"setup.py", ProjectPython, []string{".venv", "venv", "__pycache__", ".pytest_cache", ".mypy_cache", "dist", ".tox"}},
	{"pom.xml", ProjectJava, []string{"target"}},
	{"build.gradle", ProjectGradle, []string{"build", ".gradle", "out"}},
	{"build.gradle.kts", ProjectGradle, []string{"build", ".gradle", "out"}},
	{"settings.gradle", ProjectGradle, []string{"build", ".gradle", "out"}},
	{"settings.gradle.kts", ProjectGradle, []string{"build", ".gradle", "out"}},
	{"composer.json", ProjectPHP, []string{"vendor"}},
	{"Gemfile", ProjectRuby, []string{filepath.Join("vendor", "bundle")}},
	{"mix.exs", ProjectElixir, []string{"_build", "deps"}},
	{"CMakeLists.txt", ProjectCMake, []string{"build", "cmake-build-debug", "cmake-build-release"}},
	{"Package.swift", ProjectSwift, []string{".build", "Pods"}},
	{"Podfile", ProjectSwift, []string{".build", "Pods"}},
	{"pubspec.yaml", ProjectDart, []string{".dart_tool", "build"}},
}

// FindProjects обходит дерево вниз от root и находит корни проектов по маркерным файлам.
// В папки артефактов уже найденных проектов не заходит.
// maxDepth: 0 — без ограничения; N > 0 — максимальная глубина относительно root (root = 0).
func FindProjects(root string, verbose bool, maxDepth int) ([]Project, error) {
	return findProjects(root, verbose, maxDepth, nil)
}

// FindProjectsWithCallback то же, что FindProjects; onDir вызывается для каждой посещаемой директории.
func FindProjectsWithCallback(root string, verbose bool, maxDepth int, onDir func(string)) ([]Project, error) {
	return findProjects(root, verbose, maxDepth, onDir)
}

func findProjects(root string, verbose bool, maxDepth int, onDir func(string)) ([]Project, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения абсолютного пути: %w", err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("ошибка доступа к пути %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("путь не является директорией: %s", absRoot)
	}

	var projects []Project
	skipArtifacts := make(map[string]bool)
	gitRoots := make(map[string]bool)

	logf := func(format string, args ...interface{}) {
		if verbose {
			fmt.Printf(format, args...)
		}
	}

	if maxDepth > 0 {
		logf("Максимальная глубина: %d\n", maxDepth)
	}

	cleanIgn := NewCleanIgnore(absRoot)
	cleanIgn.LoadDir(absRoot)

	visitDir := func(path string) {
		if onDir != nil {
			onDir(path)
		}
	}
	visitDir(absRoot)

	// Корень (глубина 0)
	if p, ok := detectProject(absRoot); ok {
		projects = append(projects, p)
		for _, a := range p.Artifacts {
			skipArtifacts[a] = true
		}
		logProjectArtifacts(logf, p)
	} else if IsGitRepo(absRoot) {
		projects = append(projects, Project{Root: absRoot, Kinds: []ProjectKind{ProjectGit}})
		gitRoots[absRoot] = true
	}

	err = doublestar.GlobWalk(os.DirFS(absRoot), "**",
		func(path string, d fs.DirEntry) error {
			if !d.IsDir() {
				return nil
			}
			fullPath := filepath.Join(absRoot, path)
			visitDir(fullPath)
			if repo, ok := underGitRepo(fullPath, gitRoots); ok && fullPath != repo {
				return doublestar.SkipDir
			}
			depth := relativeDepth(absRoot, fullPath)

			if maxDepth > 0 && depth > maxDepth {
				return doublestar.SkipDir
			}

			base := filepath.Base(fullPath)
			if base == ".git" || base == ".svn" || base == ".hg" {
				return doublestar.SkipDir
			}

			if skipArtifacts[fullPath] {
				return doublestar.SkipDir
			}

			if !hasDirReadPermission(fullPath) {
				logf("Пропускаем директорию (нет доступа): %s\n", fullPath)
				return doublestar.SkipDir
			}

			cleanIgn.LoadDir(fullPath)
			if fullPath != absRoot && cleanIgn.Match(fullPath, true) {
				logf("cleanignore: %s\n", fullPath)
				return doublestar.SkipDir
			}

			// Корень уже обработан
			if path == "." || path == "" {
				if maxDepth > 0 && depth >= maxDepth {
					return doublestar.SkipDir
				}
				return nil
			}

			if p, ok := detectProject(fullPath); ok {
				projects = append(projects, p)
				for _, a := range p.Artifacts {
					skipArtifacts[a] = true
				}
				logProjectArtifacts(logf, p)
			} else if IsGitRepo(fullPath) {
				projects = append(projects, Project{Root: fullPath, Kinds: []ProjectKind{ProjectGit}})
				gitRoots[fullPath] = true
				return doublestar.SkipDir
			}

			if maxDepth > 0 && depth >= maxDepth {
				return doublestar.SkipDir
			}
			return nil
		},
		doublestar.WithNoFollow(),
	)
	if err != nil {
		return projects, err
	}
	return projects, nil
}

// IsGitRepo reports whether dir is a git repository root (.git exists).
func IsGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

func underGitRepo(path string, gitRoots map[string]bool) (string, bool) {
	sep := string(filepath.Separator)
	for root := range gitRoots {
		if path == root || strings.HasPrefix(path, root+sep) {
			return root, true
		}
	}
	return "", false
}

// relativeDepth возвращает глубину path относительно root (root → 0).
func relativeDepth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == "" {
		return 0
	}
	return len(strings.Split(filepath.ToSlash(rel), "/"))
}

func logProjectArtifacts(logf func(string, ...interface{}), p Project) {
	if len(p.Artifacts) == 0 {
		return
	}
	var total int64
	for _, a := range p.Artifacts {
		size, _ := DirSize(a)
		total += size
	}
	logf("%s [%s]: %s\n", p.Root, kindsJoin(p.Kinds), humanize.Bytes(uint64(total)))
}

func kindsJoin(kinds []ProjectKind) string {
	parts := make([]string, len(kinds))
	for i, k := range kinds {
		parts[i] = string(k)
	}
	return strings.Join(parts, "+")
}

// detectProject проверяет маркеры в dir и возвращает Project с существующими артефактами.
func detectProject(dir string) (Project, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Project{}, false
	}

	nameSet := make(map[string]bool, len(entries))
	for _, e := range entries {
		nameSet[e.Name()] = true
	}

	kindSet := make(map[ProjectKind]bool)
	artifactSet := make(map[string]bool)

	for _, rule := range markerRules {
		if nameSet[rule.name] {
			kindSet[rule.kind] = true
			for _, rel := range rule.arts {
				artifactSet[filepath.Join(dir, rel)] = true
			}
		}
	}

	// .NET: *.csproj / *.sln / *.fsproj
	for name := range nameSet {
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".csproj") || strings.HasSuffix(lower, ".sln") || strings.HasSuffix(lower, ".fsproj") {
			kindSet[ProjectDotnet] = true
			artifactSet[filepath.Join(dir, "bin")] = true
			artifactSet[filepath.Join(dir, "obj")] = true
			break
		}
	}

	if len(kindSet) == 0 {
		return Project{}, false
	}

	kinds := make([]ProjectKind, 0, len(kindSet))
	for k := range kindSet {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })

	var artifacts []string
	for abs := range artifactSet {
		if info, err := os.Stat(abs); err == nil && info.IsDir() {
			artifacts = append(artifacts, abs)
		}
	}
	sort.Strings(artifacts)

	return Project{
		Root:      dir,
		Kinds:     kinds,
		Artifacts: artifacts,
	}, true
}

// hasDirReadPermission проверяет возможность чтения директории.
func hasDirReadPermission(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		return false
	}
	if info.Mode()&0400 == 0 {
		return false
	}
	_, err = os.ReadDir(path)
	return err == nil
}

// DirSize возвращает суммарный размер файлов в директории.
func DirSize(root string) (int64, error) {
	info, err := os.Stat(root)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return info.Size(), nil
	}
	var total int64
	err = WalkTree(context.Background(), root, func(_ string, d fs.DirEntry) error {
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		total += fi.Size()
		return nil
	})
	return total, err
}
