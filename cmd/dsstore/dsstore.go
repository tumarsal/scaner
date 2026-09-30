package dsstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

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
		Use:   "dsstore [путь]",
		Short: "Удаляет файлы .DS_Store",
		Long: `Ищет файлы .DS_Store в указанном дереве каталогов и удаляет их.

По умолчанию только показывает, что будет удалено (dry-run).
Реальное удаление — только с флагом -f / --force.
Глубину можно ограничить через --maxdepth.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runDSStore,
	}
	cmd.Flags().StringVarP(&Path, "path", "p", ".", "Путь к директории для поиска .DS_Store")
	cmd.Flags().BoolVarP(&Verbose, "verbose", "v", false, "Подробный вывод")
	cmd.Flags().BoolVarP(&Force, "force", "f", false, "Реально удалить файлы (без флага — только dry-run)")
	cmd.Flags().IntVar(&MaxDepth, "maxdepth", 0, "Максимальная глубина обхода относительно корня (0 = без ограничения)")
	return cmd
}

func runDSStore(cmd *cobra.Command, args []string) error {
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
		fmt.Printf("Поиск .DS_Store в: %s\n", absPath)
		if MaxDepth > 0 {
			fmt.Printf("Максимальная глубина: %d\n", MaxDepth)
		}
		if Force {
			fmt.Println("Режим: удаление (-f)")
		} else {
			fmt.Println("Режим: dry-run (для удаления укажите -f)")
		}
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	files, err := scaner.FindDSStore(ctx, absPath, MaxDepth)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("поиск .DS_Store прерван: %w", err)
		}
		return fmt.Errorf("ошибка поиска .DS_Store: %w", err)
	}

	if len(files) == 0 {
		fmt.Println("Файлы .DS_Store не найдены.")
		return nil
	}

	if !Force {
		fmt.Println("Dry-run (ничего не удаляется). Для удаления: -f")
		fmt.Println()
	}

	var totalSize int64
	removed := 0
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("поиск .DS_Store прерван: %w", err)
		}
		action := "будет удалено"
		if Force {
			if err := os.Remove(f.Path); err != nil {
				fmt.Printf("ОШИБКА %s (%s): %v\n", f.Path, humanize.Bytes(uint64(f.Size)), err)
				continue
			}
			action = "удалено"
			removed++
		}
		totalSize += f.Size
		fmt.Printf("%s: %s (%s)\n", action, f.Path, humanize.Bytes(uint64(f.Size)))
	}

	fmt.Println()
	if Force {
		fmt.Printf("Итого: удалено файлов %d, освобождено %s\n",
			removed, humanize.Bytes(uint64(totalSize)))
	} else {
		fmt.Printf("Итого: файлов %d, можно освободить %s\n",
			len(files), humanize.Bytes(uint64(totalSize)))
	}
	return nil
}
