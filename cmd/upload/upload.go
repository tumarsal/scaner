package upload

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tumarsal/scaner/archive"
	"github.com/tumarsal/scaner/secretstore"
)

var (
	Path         string
	Host         string
	ZipSizeLimit int64
	Password     string
	Verbose      bool
	ChunkSize    int64
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upload [путь]",
		Short: "Упаковывает файл или папку в zip и выгружает на сервер",
		Long:  `Упаковывает файл или папку в zip и выгружает на сервер. Для папки при --zip-size-limit разбиение по размеру: дочерние папки — отдельные zip.`,
		Args:  cobra.MaximumNArgs(1),
		RunE:  runUpload,
	}
	cmd.Flags().StringVarP(&Path, "path", "p", ".", "Путь к файлу или папке для выгрузки")
	cmd.Flags().StringVar(&Host, "host", "https://ckptcli.smartapi.ru", "URL сервера (обязательно)")
	cmd.Flags().Int64Var(&ZipSizeLimit, "zip-size-limit", 1<<30, "Лимит размера zip в байтах (по умолчанию 1 ГБ); при превышении дочерние папки выгружаются отдельно")
	cmd.Flags().StringVar(&Password, "password", "", "Пароль для архива (опционально)")
	cmd.Flags().BoolVarP(&Verbose, "verbose", "v", false, "Подробный вывод")
	cmd.Flags().Int64Var(&ChunkSize, "chunk-size", 5*1024*1024, "Размер части выгрузки в байтах (по умолчанию 5 MB; при 413 уменьшите)")
	return cmd
}

func runUpload(cmd *cobra.Command, args []string) error {
	path := Path
	if len(args) > 0 {
		path = args[0]
	}
	if Host == "" {
		return fmt.Errorf("не указан --host")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("ошибка пути: %w", err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("путь недоступен: %w", err)
	}

	client := secretstore.NewClient(Host, Verbose, ChunkSize)
	defer client.Close()

	if !info.IsDir() && strings.EqualFold(filepath.Ext(absPath), ".zip") {
		remoteName := filepath.Base(absPath)
		if Verbose {
			fmt.Printf("Выгрузка %s -> %s (zip без перепаковки)\n", absPath, remoteName)
		}
		if err := client.UploadFolder(absPath, remoteName); err != nil {
			return fmt.Errorf("выгрузка %s: %w", remoteName, err)
		}
		if err := client.WaitForQueueEmpty(); err != nil {
			return fmt.Errorf("выгрузка не удалась: %w", err)
		}
		if Verbose {
			fmt.Println("Готово.")
		}
		return nil
	}

	tmpDir, err := os.MkdirTemp("", "scaner_upload_*")
	if err != nil {
		return fmt.Errorf("временная папка: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	zipManager := archive.NewZipsManager(tmpDir, Password, Verbose, ZipSizeLimit, true)
	if err := zipManager.ProcessOne(absPath); err != nil {
		return fmt.Errorf("ошибка постановки в очередь: %w", err)
	}
	zipManager.Close()

	for _, res := range zipManager.ArchivesWithPaths() {
		if Verbose {
			fmt.Printf("Выгрузка %s -> %s\n", res.LocalPath, res.FilePath)
		}
		if err := client.UploadFolder(res.LocalPath, res.FilePath); err != nil {
			return fmt.Errorf("выгрузка %s: %w", res.FilePath, err)
		}
	}
	if err := client.WaitForQueueEmpty(); err != nil {
		return fmt.Errorf("выгрузка не удалась: %w", err)
	}
	_ = zipManager.DeleteCreatedArchives()
	if Verbose {
		fmt.Println("Готово.")
	}
	return nil
}
