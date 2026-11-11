package tools

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// gitignore is a subset of git's rules: comments, blank lines, negation,
// a trailing slash for directories, a leading slash to anchor at the
// .gitignore's directory, and globs where a single * does not cross '/'.
// ** crosses directories. The last matching pattern wins, so a later
// !keep.log can undo *.log. A file inside a directory that was already
// skipped cannot be brought back; git has the same limit.
type gitignore struct {
	sets []ignoreSet
	seen map[string]bool
}

type ignoreSet struct {
	base string // workspace-relative directory, "" for the root, slash-separated
	pats []ignorePat
}

type ignorePat struct {
	negate   bool
	dirOnly  bool
	anchored bool
	re       *regexp.Regexp
}

func newGitignore() *gitignore {
	return &gitignore{seen: map[string]bool{}}
}

// load reads dir/.gitignore. rel is the workspace-relative directory.
func (g *gitignore) load(dir, rel string) {
	rel = filepath.ToSlash(rel)
	if rel == "." {
		rel = ""
	}
	key := rel
	if g.seen[key] {
		return
	}
	g.seen[key] = true
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		return
	}
	pats := parseGitignore(string(data))
	if len(pats) == 0 {
		return
	}
	g.sets = append(g.sets, ignoreSet{base: rel, pats: pats})
}

// ignored reports whether a workspace-relative path should be skipped.
func (g *gitignore) ignored(rel string, isDir bool) bool {
	rel = filepath.ToSlash(rel)
	ignored := false
	for _, set := range g.sets {
		local, ok := localPath(set.base, rel)
		if !ok || local == "" {
			continue
		}
		for _, p := range set.pats {
			if p.match(local, isDir) {
				ignored = !p.negate
			}
		}
	}
	return ignored
}

func localPath(base, rel string) (string, bool) {
	if base == "" {
		return rel, true
	}
	if rel == base {
		return "", true
	}
	prefix := base + "/"
	if strings.HasPrefix(rel, prefix) {
		return strings.TrimPrefix(rel, prefix), true
	}
	return "", false
}

func parseGitignore(text string) []ignorePat {
	var out []ignorePat
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, `\`) && len(line) > 1 && (line[1] == '#' || line[1] == '!') {
			line = line[1:]
		}
		neg := false
		if strings.HasPrefix(line, "!") {
			neg = true
			line = strings.TrimSpace(line[1:])
		}
		if line == "" {
			continue
		}
		dirOnly := strings.HasSuffix(line, "/")
		line = strings.TrimSuffix(line, "/")
		anchored := strings.HasPrefix(line, "/")
		line = strings.TrimPrefix(line, "/")
		if strings.Contains(line, "/") {
			anchored = true
		}
		if line == "" {
			continue
		}
		out = append(out, ignorePat{
			negate:   neg,
			dirOnly:  dirOnly,
			anchored: anchored,
			re:       regexp.MustCompile(globToRegex(line)),
		})
	}
	return out
}

func (p ignorePat) match(local string, isDir bool) bool {
	if p.dirOnly && !isDir {
		return false
	}
	if p.anchored {
		return p.re.MatchString(local)
	}
	base := local
	if i := strings.LastIndex(local, "/"); i >= 0 {
		base = local[i+1:]
	}
	return p.re.MatchString(base)
}

// globToRegex anchors a gitignore glob. * does not match '/', and a leading
// ** / matches any number of directories, including none.
func globToRegex(pat string) string {
	var b strings.Builder
	b.WriteByte('^')
	for i := 0; i < len(pat); {
		if strings.HasPrefix(pat[i:], "**/") {
			b.WriteString("(?:.*/)?")
			i += 3
			continue
		}
		if pat[i:] == "**" {
			b.WriteString(".*")
			break
		}
		if pat[i] == '\\' && i+1 < len(pat) {
			i++
			b.WriteString(regexp.QuoteMeta(pat[i : i+1]))
			i++
			continue
		}
		switch pat[i] {
		case '*':
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(pat[i : i+1]))
		}
		i++
	}
	b.WriteByte('$')
	return b.String()
}
