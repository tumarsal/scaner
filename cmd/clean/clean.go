package clean

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
	"github.com/tumarsal/scaner"
)

var (
	Path     string
	Verbose  bool
	Force    bool
	MaxDepth int
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clean [путь]",
		Short: "Удаляет папки зависимостей и результатов компиляции в найденных проектах",
		Long: `Ищет проекты разработки (Node, Go, Rust, Python, Java и др.) по маркерным файлам
и удаляет папки зависимостей/сборки (node_modules, target, vendor, .venv и т.д.).

По умолчанию только показывает, что будет удалено (dry-run).
Реальное удаление — только с флагом -f / --force.
Обход идёт вглубь от указанного пути (в т.ч. /Volumes/...).
Глубину можно ограничить через --maxdepth.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runClean,
	}
	cmd.Flags().StringVarP(&Path, "path", "p", ".", "Путь к директории для поиска проектов")
	cmd.Flags().BoolVarP(&Verbose, "verbose", "v", false, "Подробный вывод")
	cmd.Flags().BoolVarP(&Force, "force", "f", false, "Реально удалить артефакты (без флага — только dry-run)")
	cmd.Flags().IntVar(&MaxDepth, "maxdepth", 0, "Максимальная глубина обхода относительно корня (0 = без ограничения)")
	return cmd
}

func runClean(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		Path = args[0]
	}

	absPath, err := filepath.Abs(Path)
	if err != nil {
		return fmt.Errorf("ошибка получения абсолютного пути: %w", err)
	}
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return fmt.Errorf("директория не существует: %s", absPath)
	}

	if Verbose {
		fmt.Printf("Поиск проектов в: %s\n", absPath)
		if MaxDepth > 0 {
			fmt.Printf("Максимальная глубина: %d\n", MaxDepth)
		}
		if Force {
			fmt.Println("Режим: удаление (-f)")
		} else {
			fmt.Println("Режим: dry-run (для удаления укажите -f)")
		}
	}

	projects, err := scaner.FindProjects(absPath, Verbose, MaxDepth)
	if err != nil {
		return fmt.Errorf("ошибка поиска проектов: %w", err)
	}

	if len(projects) == 0 {
		fmt.Println("Проекты не найдены.")
		return nil
	}

	type item struct {
		project  string
		kinds    string
		artifact string
		size     int64
	}

	var items []item
	var totalSize int64
	artifactCount := 0

	for _, p := range projects {
		kinds := kindsString(p.Kinds)
		for _, art := range p.Artifacts {
			size, _ := scaner.DirSize(art)
			items = append(items, item{
				project:  p.Root,
				kinds:    kinds,
				artifact: art,
				size:     size,
			})
			totalSize += size
			artifactCount++
		}
	}

	if artifactCount == 0 {
		fmt.Printf("Найдено проектов: %d, артефактов для очистки нет.\n", len(projects))
		return nil
	}

	if !Force {
		fmt.Println("Dry-run (ничего не удаляется). Для удаления: -f")
		fmt.Println()
	}

	for _, it := range items {
		action := "будет удалено"
		if Force {
			if err := os.RemoveAll(it.artifact); err != nil {
				fmt.Printf("ОШИБКА %s (%s): %v\n", it.artifact, humanize.Bytes(uint64(it.size)), err)
				continue
			}
			action = "удалено"
		}
		fmt.Printf("[%s] %s\n  %s: %s (%s)\n", it.kinds, it.project, action, it.artifact, humanize.Bytes(uint64(it.size)))
	}

	fmt.Println()
	if Force {
		fmt.Printf("Итого: проектов %d, удалено папок %d, освобождено %s\n",
			len(projects), artifactCount, humanize.Bytes(uint64(totalSize)))
	} else {
		fmt.Printf("Итого: проектов %d, папок %d, можно освободить %s\n",
			len(projects), artifactCount, humanize.Bytes(uint64(totalSize)))
	}
	return nil
}

func kindsString(kinds []scaner.ProjectKind) string {
	parts := make([]string, len(kinds))
	for i, k := range kinds {
		parts[i] = string(k)
	}
	return strings.Join(parts, "+")
}
