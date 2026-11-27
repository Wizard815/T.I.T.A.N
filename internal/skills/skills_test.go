package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wizard815/titan/internal/config"
)

func TestTitan_SkillsLoadAndFilterByRole(t *testing.T) {
	set, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if len(set) < 8 {
		t.Fatalf("built-in skills: %d", len(set))
	}
	names := func(l []Skill) map[string]bool {
		m := map[string]bool{}
		for _, s := range l {
			m[s.Name] = true
		}
		return m
	}
	friend, family, owner := names(set.For(config.Friend)), names(set.For(config.Family)), names(set.For(config.Owner))
	if !friend["game-server"] || friend["faith-down"] || friend["container-unhealthy"] {
		t.Fatalf("friend: %v", friend)
	}
	if !family["faith-down"] || family["container-unhealthy"] || !owner["container-unhealthy"] {
		t.Fatalf("family/owner: %v / %v", family, owner)
	}

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "game.md"), []byte("---\nname: game-server\ndescription: ours\n---\nUse AMP first.\n"), 0o644)
	set, err = Load(dir)
	if err != nil || set["game-server"].Body != "Use AMP first." || set["game-server"].Audience != config.Friend {
		t.Fatalf("override: %v %+v", err, set["game-server"])
	}
	for _, bad := range []string{"no front", "---\nname: x\n---\nbody", "---\nname: a b\ndescription: d\n---\n", "---\nname: x\ndescription: d\naudience: aliens\n---\n"} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("should fail: %q", bad)
		}
	}
}
