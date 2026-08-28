package scaner

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/bmatcuk/doublestar/v4"
)

// SkipDir tells WalkTree to skip descending into the current directory.
var SkipDir = fs.SkipDir

// Walk обходит дерево от root по glob-шаблону (например "**/*.md")
// и вызывает fn для каждого найденного файла.
// Если root — файл, совпадающий с шаблоном, fn вызывается только для него.
func Walk(root, pattern string, fn func(path string) error) error {
	if fn == nil {
		return fmt.Errorf("walk callback is nil")
	}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		matched, err := doublestar.PathMatch(pattern, filepath.ToSlash(info.Name()))
		if err != nil {
			return err
		}
		if matched {
			return fn(root)
		}
		return nil
	}
	return doublestar.GlobWalk(os.DirFS(root), pattern,
		func(p string, d fs.DirEntry) error {
			if d.IsDir() {
				return nil
			}
			return fn(filepath.Join(root, p))
		},
		doublestar.WithNoFollow(),
	)
}

// WalkTree обходит всё дерево каталогов от root через doublestar.GlobWalk.
// fn вызывается для каждого файла и каталога под root (сам root не передаётся).
// Чтобы не заходить в каталог, верните SkipDir.
// Ошибки доступа к отдельным записям пропускаются (fn не вызывается).
func WalkTree(root string, fn func(path string, d fs.DirEntry) error) error {
	if fn == nil {
		return fmt.Errorf("walk callback is nil")
	}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("root is not a directory: %s", root)
	}
	root = filepath.Clean(root)
	return doublestar.GlobWalk(os.DirFS(root), "**",
		func(p string, d fs.DirEntry) error {
			if p == "." || p == "" {
				return nil
			}
			full := filepath.Join(root, filepath.FromSlash(p))
			err := fn(full, d)
			if err == nil {
				return nil
			}
			if errors.Is(err, fs.SkipDir) || errors.Is(err, doublestar.SkipDir) {
				return doublestar.SkipDir
			}
			return err
		},
		doublestar.WithNoFollow(),
	)
}
