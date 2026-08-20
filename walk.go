package scaner

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/bmatcuk/doublestar/v4"
)

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
