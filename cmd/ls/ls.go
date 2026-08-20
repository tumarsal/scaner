package ls

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tumarsal/scaner/secretstore"
)

var (
	Host     string
	Password string
	IP       string
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "Показывает дерево удалённого хранилища с датой обновления",
		Long:  `Выводит дерево файлов с сервера (все IP или один с --ip) с датой последнего обновления каждого файла.`,
		RunE:  runLs,
	}
	cmd.Flags().StringVar(&Host, "host", "https://ckptcli.smartapi.ru/", "URL сервера")
	cmd.Flags().StringVar(&Password, "password", "", "Пароль для доступа к хранилищу (обязательно)")
	cmd.Flags().StringVar(&IP, "ip", "", "Показать только дерево для указанного IP (по умолчанию — все)")
	return cmd
}

type treeNode struct {
	name     string
	updated  time.Time
	children map[string]*treeNode
}

func runLs(cmd *cobra.Command, args []string) error {
	if Password == "" {
		return fmt.Errorf("не указан --password")
	}

	client := secretstore.NewClient(Host, false, 0)
	defer client.Close()

	var files []secretstore.FileInfo
	var err error
	if IP != "" {
		files, err = client.ListFilesByIP(IP, Password)
	} else {
		files, err = client.ListFiles(Password)
	}
	if err != nil {
		return fmt.Errorf("список файлов: %w", err)
	}

	if len(files) == 0 {
		fmt.Println("Файлов не найдено.")
		return nil
	}

	byIP := make(map[string][]secretstore.FileInfo)
	for _, f := range files {
		byIP[f.IP] = append(byIP[f.IP], f)
	}
	ips := make([]string, 0, len(byIP))
	for ip := range byIP {
		ips = append(ips, ip)
	}
	sort.Strings(ips)

	const dateFmt = "2006-01-02 15:04"

	for _, ip := range ips {
		root := &treeNode{name: ip, children: make(map[string]*treeNode)}
		for _, f := range byIP[ip] {
			path := filepath.Clean(f.FilePath)
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			parts := strings.Split(strings.Trim(path, "/"), "/")
			if len(parts) == 0 || (len(parts) == 1 && parts[0] == "") {
				continue
			}
			addToTree(root, parts, f.Uploaded)
		}
		propagateDate(root)
		fmt.Println(root.name)
		printTree(root, "", true, dateFmt)
	}
	return nil
}

func addToTree(n *treeNode, parts []string, t time.Time) {
	if len(parts) == 0 {
		return
	}
	name := parts[0]
	if n.children == nil {
		n.children = make(map[string]*treeNode)
	}
	child, ok := n.children[name]
	if !ok {
		child = &treeNode{name: name}
		n.children[name] = child
	}
	if len(parts) == 1 {
		if t.After(child.updated) {
			child.updated = t
		}
		return
	}
	addToTree(child, parts[1:], t)
}

func propagateDate(n *treeNode) {
	for _, c := range n.children {
		propagateDate(c)
		if c.updated.After(n.updated) {
			n.updated = c.updated
		}
	}
}

func printTree(n *treeNode, prefix string, last bool, dateFmt string) {
	names := make([]string, 0, len(n.children))
	for name := range n.children {
		names = append(names, name)
	}
	sort.Strings(names)
	for i, name := range names {
		child := n.children[name]
		isLast := i == len(names)-1
		conn := "├── "
		if isLast {
			conn = "└── "
		}
		dateStr := ""
		if !child.updated.IsZero() {
			dateStr = "  " + child.updated.Format(dateFmt)
		}
		fmt.Printf("%s%s%s%s\n", prefix, conn, name, dateStr)
		nextPrefix := prefix
		if last {
			nextPrefix += "    "
		} else {
			nextPrefix += "│   "
		}
		printTree(child, nextPrefix, isLast, dateFmt)
	}
}
