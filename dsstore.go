package scaner

import (
	"context"
	"io/fs"
	"path/filepath"
)

// DSStoreFile — найденный файл .DS_Store.
type DSStoreFile struct {
	Path string
	Size int64
}

// FindDSStore ищет файлы .DS_Store в дереве от root.
// maxDepth: 0 — без ограничения; N > 0 — максимальная глубина относительно root (root = 0).
// Каталоги .git / .svn / .hg пропускаются.
// Поиск прекращается, если ctx отменён.
func FindDSStore(ctx context.Context, root string, maxDepth int) ([]DSStoreFile, error) {
	var files []DSStoreFile
	err := WalkTree(ctx, root, func(path string, d fs.DirEntry) error {
		depth := relativeDepth(root, path)
		if maxDepth > 0 && depth > maxDepth {
			if d.IsDir() {
				return SkipDir
			}
			return nil
		}
		if d.IsDir() {
			switch filepath.Base(path) {
			case ".git", ".svn", ".hg":
				return SkipDir
			}
			return nil
		}
		if d.Name() != ".DS_Store" {
			return nil
		}
		var size int64
		if fi, err := d.Info(); err == nil {
			size = fi.Size()
		}
		files = append(files, DSStoreFile{Path: path, Size: size})
		return nil
	})
	return files, err
}
