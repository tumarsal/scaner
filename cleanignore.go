package scaner

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const cleanIgnoreFilename = ".cleanignore"

// CleanIgnore хранит правила из .cleanignore (синтаксис как у .gitignore).
type CleanIgnore struct {
	root   string
	loaded map[string]bool
	byDir  map[string]*cleanIgnoreRules
}

type cleanIgnoreRules struct {
	basePath string
	patterns []cleanIgnoreMatcher
}

type cleanIgnoreMatcher interface {
	matches(path string, isDir bool) bool
	negated() bool
}

type cleanIgnoreBase struct {
	isNegated    bool
	matchDirOnly bool
	leadingSlash bool
	content      string
}

type cleanIgnoreSimple struct{ cleanIgnoreBase }
type cleanIgnoreFile struct{ cleanIgnoreBase }
type cleanIgnorePath struct {
	cleanIgnoreBase
	depth int
}
type cleanIgnoreRegex struct {
	cleanIgnoreBase
	re *regexp.Regexp
}

func (b cleanIgnoreBase) negated() bool { return b.isNegated }

// NewCleanIgnore создаёт matcher для обхода от root.
func NewCleanIgnore(root string) *CleanIgnore {
	abs, _ := filepath.Abs(root)
	return &CleanIgnore{
		root:   abs,
		loaded: make(map[string]bool),
		byDir:  make(map[string]*cleanIgnoreRules),
	}
}

// LoadDir читает dir/.cleanignore, если файл есть (кэшируется).
func (c *CleanIgnore) LoadDir(dir string) {
	abs, err := filepath.Abs(dir)
	if err != nil || abs == "" {
		return
	}
	if c.loaded[abs] {
		return
	}
	c.loaded[abs] = true

	ignorePath := filepath.Join(abs, cleanIgnoreFilename)
	if _, err := os.Stat(ignorePath); err != nil {
		return
	}
	rules, err := parseCleanIgnoreFile(ignorePath, abs)
	if err != nil {
		return
	}
	c.byDir[abs] = rules
}

// Match возвращает true, если absPath нужно исключить из обхода.
// Корень поиска сам по себе не исключается.
func (c *CleanIgnore) Match(absPath string, isDir bool) bool {
	full, err := filepath.Abs(absPath)
	if err != nil {
		return false
	}
	if full == c.root {
		return false
	}
	if !strings.HasPrefix(full, c.root+string(filepath.Separator)) && full != c.root {
		return false
	}

	c.ensureChainLoaded(full)

	var ruleSets []*cleanIgnoreRules
	cur := c.root
	ruleSets = appendRuleSet(ruleSets, c.byDir[cur])
	rel, err := filepath.Rel(c.root, full)
	if err != nil || rel == "." {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		ruleSets = appendRuleSet(ruleSets, c.byDir[cur])
	}

	ignore := false
	matched := false
	for _, rs := range ruleSets {
		if rs == nil {
			continue
		}
		testPath, err := filepath.Rel(rs.basePath, full)
		if err != nil {
			continue
		}
		if testPath == "." {
			testPath = ""
		} else {
			testPath = filepath.ToSlash(testPath)
		}
		for _, p := range rs.patterns {
			if p.matches(testPath, isDir) {
				ignore = !p.negated()
				matched = true
			}
		}
	}
	return matched && ignore
}

func appendRuleSet(list []*cleanIgnoreRules, rs *cleanIgnoreRules) []*cleanIgnoreRules {
	if rs != nil {
		return append(list, rs)
	}
	return list
}

func (c *CleanIgnore) ensureChainLoaded(full string) {
	cur := c.root
	c.LoadDir(cur)
	rel, err := filepath.Rel(c.root, full)
	if err != nil || rel == "." {
		return
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		c.LoadDir(cur)
	}
}

func parseCleanIgnoreFile(path, basePath string) (*cleanIgnoreRules, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	rules := &cleanIgnoreRules{basePath: basePath}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		rules.addPattern(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return rules, nil
}

func (r *cleanIgnoreRules) addPattern(pattern string) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || strings.HasPrefix(pattern, "#") {
		return
	}

	negated := false
	matchDirOnly := false
	leadingSlash := false

	if strings.HasPrefix(pattern, "!") {
		negated = true
		pattern = pattern[1:]
	} else if strings.HasPrefix(pattern, `\!`) {
		pattern = pattern[1:]
	}
	if strings.HasPrefix(pattern, "/") {
		leadingSlash = true
		pattern = pattern[1:]
	}
	if strings.HasSuffix(pattern, "/") {
		matchDirOnly = true
		pattern = pattern[:len(pattern)-1]
	}
	if pattern == "" {
		return
	}

	base := cleanIgnoreBase{
		isNegated:    negated,
		matchDirOnly: matchDirOnly,
		leadingSlash: leadingSlash,
		content:      pattern,
	}

	var m cleanIgnoreMatcher
	if strings.Contains(pattern, "**") {
		m = newCleanIgnoreRegex(base)
	} else if strings.Contains(pattern, "/") || leadingSlash {
		m = newCleanIgnorePath(base)
	} else if strings.ContainsAny(pattern, "*?[") {
		m = newCleanIgnoreFilePat(base)
	} else {
		m = cleanIgnoreSimple{base}
	}
	r.patterns = append(r.patterns, m)
}

func (p cleanIgnoreSimple) matches(path string, isDir bool) bool {
	if p.matchDirOnly && !isDir {
		return false
	}
	name := filepath.Base(path)
	return name == p.content
}

func newCleanIgnoreFilePat(base cleanIgnoreBase) cleanIgnoreMatcher {
	return cleanIgnoreFile{base}
}

func (p cleanIgnoreFile) matches(path string, isDir bool) bool {
	if p.matchDirOnly && !isDir {
		return false
	}
	name := filepath.Base(path)
	ok, _ := filepath.Match(p.content, name)
	return ok
}

func newCleanIgnorePath(base cleanIgnoreBase) cleanIgnoreMatcher {
	depth := 0
	if !base.leadingSlash {
		depth = strings.Count(base.content, "/")
	}
	return cleanIgnorePath{base, depth}
}

func (p cleanIgnorePath) matches(path string, isDir bool) bool {
	if p.matchDirOnly && !isDir {
		return false
	}
	if runtime.GOOS == "windows" {
		path = filepath.ToSlash(path)
	}
	if p.leadingSlash {
		ok, _ := filepath.Match(p.content, path)
		return ok
	}
	slashes := 0
	pos := 0
	for pos = len(path) - 1; pos >= 0; pos-- {
		if path[pos:pos+1] == "/" {
			slashes++
			if slashes > p.depth {
				break
			}
		}
	}
	if slashes < p.depth {
		return false
	}
	checkPath := path[pos+1:]
	ok, _ := filepath.Match(p.content, checkPath)
	return ok
}

func newCleanIgnoreRegex(base cleanIgnoreBase) cleanIgnoreMatcher {
	matchStart := false
	matchEnd := false
	content := base.content
	if strings.HasPrefix(content, "**/") {
		content = content[3:]
	} else {
		matchStart = true
	}
	if strings.HasSuffix(content, "/**") {
		content = content[:len(content)-3]
	} else {
		matchEnd = true
	}

	parts := strings.Split(content, "**")
	for i := range parts {
		parts[i] = regexp.QuoteMeta(parts[i])
	}
	pattern := strings.Join(parts, ".*?")
	if matchStart {
		pattern = "^" + pattern
	}
	if matchEnd {
		pattern = pattern + "$"
	}
	return cleanIgnoreRegex{base, regexp.MustCompile(pattern)}
}

func (p cleanIgnoreRegex) matches(path string, isDir bool) bool {
	if p.matchDirOnly && !isDir {
		return false
	}
	if runtime.GOOS == "windows" {
		path = filepath.ToSlash(path)
	}
	return p.re.MatchString(path)
}
