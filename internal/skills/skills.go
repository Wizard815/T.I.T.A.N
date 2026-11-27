// Package skills holds T.I.T.A.N's playbooks: short Markdown notes that
// tell the helper how to look into a kind of problem ("the game server
// won't start", "my show isn't on Plex") and which named fix fits.
//
// A skill is knowledge, never power: it can't add an action, change who
// sees what, or skip the Yes. The helper reads one with read_skill; the
// person's role decides which skills it can see at all.
//
// Built-in skills live in builtin/. Wizard's own go in skills_dir (one .md
// each) and replace a built-in one with the same name.
package skills

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/wizard815/titan/internal/config"
)

//go:embed builtin/*.md
var builtin embed.FS

type Skill struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"` // when to read it; shown to the helper
	Audience    string   `yaml:"audience"`    // friends | family | owner (default friends)
	Services    []string `yaml:"services"`    // service ids it's about (informational)
	Body        string   `yaml:"-"`
}

// Set is every skill by name.
type Set map[string]Skill

// Load reads the built-in skills, then dir (if set) on top.
func Load(dir string) (Set, error) {
	out := Set{}
	if err := add(out, builtin, "builtin"); err != nil {
		return nil, err
	}
	if dir != "" {
		if _, err := os.Stat(dir); err != nil {
			return nil, fmt.Errorf("skills_dir %s: %w", dir, err)
		}
		if err := add(out, os.DirFS(dir), "."); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func add(out Set, fsys fs.FS, root string) error {
	return fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".md" {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		s, err := Parse(b)
		if err != nil {
			return fmt.Errorf("skill %s: %w", p, err)
		}
		out[s.Name] = s
		return nil
	})
}

// Parse reads "---\nfrontmatter\n---\nbody".
func Parse(b []byte) (Skill, error) {
	var s Skill
	rest, ok := bytes.CutPrefix(bytes.TrimLeft(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), "\n"), []byte("---\n"))
	if !ok {
		return s, fmt.Errorf("it must start with --- frontmatter")
	}
	front, body, ok := bytes.Cut(rest, []byte("\n---"))
	if !ok {
		return s, fmt.Errorf("the frontmatter has no closing ---")
	}
	if err := yaml.Unmarshal(front, &s); err != nil {
		return s, err
	}
	if s.Name == "" || s.Description == "" {
		return s, fmt.Errorf("name and description are needed")
	}
	if strings.ContainsAny(s.Name, " /\\") {
		return s, fmt.Errorf("name %q: use letters, digits and dashes", s.Name)
	}
	if s.Audience == "" {
		s.Audience = "friends"
	}
	r, ok := config.AudienceRole(s.Audience)
	if !ok {
		return s, fmt.Errorf("audience must be friends, family or owner, got %q", s.Audience)
	}
	s.Audience = r
	s.Body = strings.TrimSpace(strings.TrimPrefix(string(body), "\n"))
	if len(s.Body) > 4000 {
		return s, fmt.Errorf("keep a skill under 4000 characters (the model reads all of it)")
	}
	return s, nil
}

// For lists the skills a role may read, by name.
func (set Set) For(role string) []Skill {
	var out []Skill
	for _, s := range set {
		if config.AtLeast(role, s.Audience) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
