package download

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tumarsal/scaner/secretstore"
)

var (
	Host     string
	Password string
	IP       string
	Dist     string
	Filename string
	Verbose  bool
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "download",
		Short: "Скачивает все файлы с сервера для указанного IP",
		Long:  `Получает список файлов по IP с сервера и скачивает их в указанную папку. Имена файлов — как на сервере; при дубликатах добавляется суффикс _1, _2 и т.д.`,
		RunE:  runDownload,
	}
	cmd.Flags().StringVar(&Host, "host", "https://ckptcli.smartapi.ru/", "URL сервера")
	cmd.Flags().StringVar(&Password, "password", "", "Пароль для доступа к хранилищу (обязательно)")
	cmd.Flags().StringVar(&IP, "ip", "", "IP клиента, чьи файлы скачивать (обязательно)")
	cmd.Flags().StringVarP(&Dist, "dist", "d", ".", "Папка назначения для скачанных файлов")
	cmd.Flags().StringVarP(&Filename, "filename", "f", "", "Скачать только файл с указанным именем")
	cmd.Flags().BoolVarP(&Verbose, "verbose", "v", false, "Подробный вывод")
	return cmd
}

func runDownload(cmd *cobra.Command, args []string) error {
	if Password == "" {
		return fmt.Errorf("не указан --password")
	}
	if IP == "" {
		return fmt.Errorf("не указан --ip")
	}

	distPath, err := filepath.Abs(Dist)
	if err != nil {
		return fmt.Errorf("ошибка пути dist: %w", err)
	}
	if err := os.MkdirAll(distPath, 0755); err != nil {
		return fmt.Errorf("не удалось создать папку %s: %w", distPath, err)
	}

	client := secretstore.NewClient(Host, Verbose, 0)
	defer client.Close()

	files, err := client.ListFilesByIP(IP, Password)
	if err != nil {
		return fmt.Errorf("список файлов: %w", err)
	}
	if len(files) == 0 {
		fmt.Printf("Файлов для IP %s не найдено.\n", IP)
		return nil
	}

	if Filename != "" {
		found := false
		for _, f := range files {
			if f.Filename == Filename {
				found = true
				files = []secretstore.FileInfo{f}
				break
			}
		}
		if !found {
			return fmt.Errorf("файл %q не найден для IP %s", Filename, IP)
		}
	}

	nameCount := make(map[string]int)
	for _, f := range files {
		baseName := f.Filename
		if baseName == "" {
			baseName = f.FileHash
		}
		n := nameCount[baseName]
		localName := baseName
		if n > 0 {
			ext := filepath.Ext(baseName)
			noExt := strings.TrimSuffix(baseName, ext)
			localName = fmt.Sprintf("%s_%d%s", noExt, n, ext)
		}
		nameCount[baseName]++

		outPath := filepath.Join(distPath, localName)
		if Verbose {
			fmt.Printf("Скачивание %s -> %s\n", f.FileHash, localName)
		}
		if err := client.DownloadFile(f.FileHash, Password, outPath); err != nil {
			return fmt.Errorf("скачивание %s: %w", localName, err)
		}
	}
	fmt.Printf("Скачано файлов: %d в %s\n", len(files), distPath)
	return nil
}
