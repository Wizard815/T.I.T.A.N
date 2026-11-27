// Package config reads titan.yaml: which services the board shows, who may
// see them, which fixes each one allows, and which Argos models to use.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Roles, lowest first. A role sees and fixes everything a lower one can.
const (
	Friend = "friend"
	Family = "family"
	Owner  = "owner"
)

var rank = map[string]int{Friend: 1, Family: 2, Owner: 3}

// ValidRole reports whether r is friend, family or owner.
func ValidRole(r string) bool { return rank[r] > 0 }

// AtLeast reports whether role has at least the rank of need.
func AtLeast(role, need string) bool { return rank[role] > 0 && rank[role] >= rank[need] }

// Actions a service can allow. These are the only things T.I.T.A.N can do.
const (
	ActCheck   = "check_now"       // re-run the check; changes nothing
	ActStart   = "start_service"   // start a stopped container
	ActRestart = "restart_service" // restart a container
	ActSync    = "run_sync"        // call a named sync job
	ActLogs    = "recent_problems" // last log lines, secrets stripped
)

// Changing actions go through titan-hands and need a Yes from the person.
var Changing = map[string]bool{ActStart: true, ActRestart: true, ActSync: true}

var knownAction = map[string]bool{ActCheck: true, ActStart: true, ActRestart: true, ActSync: true, ActLogs: true}

type Config struct {
	Argos    Argos     `yaml:"argos"`
	Models   Models    `yaml:"models"`
	Hands    Hands     `yaml:"hands"`
	Notify   Notify    `yaml:"notify"`
	FaithURL string    `yaml:"faith_url"`
	Kuma     Kuma      `yaml:"kuma"`
	Every    Duration  `yaml:"check_every"`
	KeepDays int       `yaml:"keep_days"`
	Limits   Limits    `yaml:"limits"`
	Services []Service `yaml:"services"`
	Pipeline *Pipeline `yaml:"pipeline"`
	Network  *Network  `yaml:"network"`
	Style    string    `yaml:"style"` // caveman (default) or plain
	Room     Room      `yaml:"room"`
	Discover Discover  `yaml:"discover"`
}

// Discover makes cards from what titan-hands sees in Docker, so a new app
// shows up without editing this file. Services listed under services:
// win (matched by container name).
type Discover struct {
	Docker   bool     `yaml:"docker"`   // list every container
	Audience string   `yaml:"audience"` // who sees found containers; default owner (a titan.audience label overrides)
	Host     string   `yaml:"host"`     // fills [IP] in Unraid's web UI links, e.g. 192.168.20.5
	Skip     []string `yaml:"skip"`     // container names to leave out
	Stopped  bool     `yaml:"stopped"`  // also show containers that are stopped (off by default)
}

// Room is the shared chat everyone signed in can talk in.
type Room struct {
	Off   bool     `yaml:"off"`   // turn the room off
	Words []string `yaml:"words"` // more words to mask, on top of the built-in list
}

// Pipeline is the media chain: Seerr -> Sonarr/Radarr -> SABnzbd -> Plex
// (through Tautulli). Read only. Any stage can be left out.
type Pipeline struct {
	Audience string  `yaml:"audience"` // who may ask about it; default family
	Seerr    *Source `yaml:"seerr"`
	Sonarr   *Source `yaml:"sonarr"`
	Radarr   *Source `yaml:"radarr"`
	Sabnzbd  *Source `yaml:"sabnzbd"`
	Tautulli *Source `yaml:"tautulli"`
}

// Network is the owner's view of the home network (Omada). Pausing a
// device goes through titan-hands, which keeps the Omada login.
type Network struct {
	Enabled bool `yaml:"enabled"`
}

type Argos struct {
	URL       string   `yaml:"url"`         // OpenAI-compatible base, e.g. http://argos:8080/v1
	APIKeyEnv string   `yaml:"api_key_env"` // env var holding a key, if Argos wants one
	Timeout   Duration `yaml:"timeout"`
}

// Models are names Argos serves. Point them at local or cloud models in
// Argos (profiles or selectors), not here.
type Models struct {
	Guard    string `yaml:"guard"`
	Agent    string `yaml:"agent"`
	Fallback string `yaml:"fallback"`
}

type Hands struct {
	URL      string `yaml:"url"`
	TokenEnv string `yaml:"token_env"`
}

type Notify struct {
	NtfyURL string `yaml:"ntfy_url"` // optional: POST a line here for the owner
}

// Kuma is an outside uptime app T.I.T.A.N can read: Uptime Kuma (a
// published status page) or Gatus. T.I.T.A.N's own checks work without one.
type Kuma struct {
	Kind      string `yaml:"kind"` // kuma (default) or gatus
	URL       string `yaml:"url"`
	Slug      string `yaml:"slug"`
	ImportAll bool   `yaml:"import_all"` // show monitors not listed under services
	Audience  string `yaml:"audience"`   // who sees imported monitors
}

// Limits apply to changing actions, per service and action.
type Limits struct {
	PerHour int      `yaml:"per_hour"` // default 3
	Confirm Duration `yaml:"confirm_within"`
}

type Service struct {
	ID        string   `yaml:"id"`
	Name      string   `yaml:"name"`
	Group     string   `yaml:"group"`
	About     string   `yaml:"about"`
	Audience  string   `yaml:"audience"` // friends | family | owner: who sees it
	Person    string   `yaml:"person"`   // personal: only this account (and the owner) sees it
	FixBy     string   `yaml:"fix_by"`   // who may ask for fixes; default = audience
	Container string   `yaml:"container"`
	AMP       string   `yaml:"amp"`        // an AMP instance name (game servers); start/restart go through AMP, not Docker
	Sync      string   `yaml:"sync"`       // sync job name known to titan-hands
	DependsOn []string `yaml:"depends_on"` // service ids; when one is down, this card says so
	Info      []Info   `yaml:"info"`       // extra facts shown on the card
	Actions   []string `yaml:"actions"`
	Link      string   `yaml:"link"` // an "Open" button on the card
	Check     Check    `yaml:"check"`
}

type Check struct {
	HTTP      string     `yaml:"http"`      // GET; 2xx/3xx is up
	Container bool       `yaml:"container"` // the container is running
	Kuma      string     `yaml:"kuma"`      // a monitor name in the uptime app (Uptime Kuma or Gatus)
	TCP       string     `yaml:"tcp"`       // host:port answers (game servers, databases)
	Freshness *Freshness `yaml:"freshness"` // a "last ran" time must be recent
	AMP       bool       `yaml:"amp"`       // the AMP instance's game is running
	Pipeline  bool       `yaml:"pipeline"`  // the media pipeline isn't stuck
	Found     string     `yaml:"-"`         // set for discovered containers: read from the inventory
}

// Info is one extra fact on a card. Give exactly one kind. Keys are read
// from env vars named by key_env and never leave titan.
type Info struct {
	Tautulli *Source   `yaml:"tautulli"` // streams now (and how many transcode)
	Seerr    *Source   `yaml:"seerr"`    // requests waiting and in progress
	Arr      *Source   `yaml:"arr"`      // Sonarr/Radarr/Readarr download queue
	Argos    *Source   `yaml:"argos"`    // which models are loaded
	JSON     *JSONFact `yaml:"json"`     // any value from a JSON URL
}

type Source struct {
	URL    string `yaml:"url"`
	KeyEnv string `yaml:"key_env"`
	Label  string `yaml:"label"`
}

type JSONFact struct {
	URL       string `yaml:"url"`
	Field     string `yaml:"field"` // dot path
	Label     string `yaml:"label"`
	Suffix    string `yaml:"suffix"`
	HeaderEnv string `yaml:"header_env"` // "Name: value" in this env var, e.g. an API key header
}

// Freshness reads a JSON time from URL (Field is a dot path, e.g.
// "sync.last"), as RFC 3339 or Unix seconds, and is up while it is newer
// than MaxAge.
type Freshness struct {
	URL    string   `yaml:"url"`
	Field  string   `yaml:"field"`
	MaxAge Duration `yaml:"max_age"`
}

// Duration reads "90s", "6h" or "2d".
type Duration time.Duration

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := ParseDuration(n.Value)
	*d = Duration(v)
	return err
}

func (d Duration) D() time.Duration { return time.Duration(d) }

func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		var n float64
		if _, err := fmt.Sscanf(s, "%gd", &n); err != nil {
			return 0, fmt.Errorf("%q isn't a duration (try 90s, 6h or 2d)", s)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%q isn't a duration (try 90s, 6h or 2d)", s)
	}
	return v, nil
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,40}$`)

// audience words people write, mapped to roles.
// AudienceRole turns "friends", "family" or "owner" into a role.
func AudienceRole(a string) (string, bool) { return audienceRole(a) }

func audienceRole(a string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(a)) {
	case "friends", "friend", "everyone":
		return Friend, true
	case "family":
		return Family, true
	case "owner", "wizard", "":
		return Owner, true
	}
	return "", false
}

// Load reads and checks a config file.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("can't read the config at %s: %w (copy titan.example.yaml there)", path, err)
	}
	return Parse(b)
}

// Parse checks and fills defaults. Every problem names the service.
func Parse(b []byte) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("titan.yaml: %w", err)
	}
	if c.Every == 0 {
		c.Every = Duration(time.Minute)
	}
	if c.KeepDays == 0 {
		c.KeepDays = 90
	}
	if c.Limits.PerHour == 0 {
		c.Limits.PerHour = 3
	}
	if c.Limits.Confirm == 0 {
		c.Limits.Confirm = Duration(10 * time.Minute)
	}
	if c.Argos.Timeout == 0 {
		c.Argos.Timeout = Duration(90 * time.Second)
	}
	if c.Models.Guard == "" {
		c.Models.Guard = "titan-guard"
	}
	if c.Models.Agent == "" {
		c.Models.Agent = "titan-agent"
	}
	if c.Hands.TokenEnv == "" {
		c.Hands.TokenEnv = "TITAN_HANDS_TOKEN"
	}
	switch c.Kuma.Kind {
	case "":
		c.Kuma.Kind = "kuma"
	case "kuma", "gatus":
	default:
		return nil, fmt.Errorf("kuma.kind must be kuma or gatus, got %q", c.Kuma.Kind)
	}
	if r, ok := audienceRole(c.Discover.Audience); ok {
		c.Discover.Audience = r
	} else {
		return nil, fmt.Errorf("discover.audience must be friends, family or owner, got %q", c.Discover.Audience)
	}
	if c.Kuma.Kind == "kuma" && c.Kuma.URL != "" && c.Kuma.Slug == "" {
		return nil, fmt.Errorf("kuma.slug is needed: the name of a published Uptime Kuma status page")
	}
	if c.Kuma.Audience != "" {
		r, ok := audienceRole(c.Kuma.Audience)
		if !ok {
			return nil, fmt.Errorf("kuma.audience must be friends, family or owner, got %q", c.Kuma.Audience)
		}
		c.Kuma.Audience = r
	} else {
		c.Kuma.Audience = Owner
	}
	switch c.Style {
	case "", "caveman", "plain":
	default:
		return nil, fmt.Errorf("style must be caveman or plain, got %q", c.Style)
	}
	if p := c.Pipeline; p != nil {
		if p.Audience == "" {
			p.Audience = Family
		} else if r, ok := audienceRole(p.Audience); ok {
			p.Audience = r
		} else {
			return nil, fmt.Errorf("pipeline.audience must be friends, family or owner, got %q", p.Audience)
		}
		for name, src := range map[string]*Source{"seerr": p.Seerr, "sonarr": p.Sonarr, "radarr": p.Radarr, "sabnzbd": p.Sabnzbd, "tautulli": p.Tautulli} {
			if src != nil && src.URL == "" {
				return nil, fmt.Errorf("pipeline.%s needs a url", name)
			}
		}
	}
	seen := map[string]bool{}
	for i := range c.Services {
		s := &c.Services[i]
		if !idPattern.MatchString(s.ID) {
			return nil, fmt.Errorf("services[%d]: id must be lowercase letters, digits, - or _ (like minecraft), got %q", i, s.ID)
		}
		if seen[s.ID] {
			return nil, fmt.Errorf("service %s is listed twice", s.ID)
		}
		seen[s.ID] = true
		if s.Name == "" {
			s.Name = s.ID
		}
		if s.Group == "" {
			s.Group = "Services"
		}
		r, ok := audienceRole(s.Audience)
		if !ok {
			return nil, fmt.Errorf("service %s: audience must be friends, family or owner, got %q", s.ID, s.Audience)
		}
		s.Audience = r
		if s.FixBy == "" {
			s.FixBy = s.Audience
		} else if r, ok = audienceRole(s.FixBy); !ok {
			return nil, fmt.Errorf("service %s: fix_by must be friends, family or owner, got %q", s.ID, s.FixBy)
		} else {
			s.FixBy = r
		}
		if rank[s.FixBy] < rank[s.Audience] {
			return nil, fmt.Errorf("service %s: fix_by can't be wider than audience (people can't fix what they can't see)", s.ID)
		}
		s.Person = strings.ToLower(strings.TrimSpace(s.Person))
		n := 0
		if s.Check.HTTP != "" {
			n++
		}
		if s.Check.Container {
			n++
		}
		if s.Check.Kuma != "" {
			n++
		}
		if s.Check.TCP != "" {
			n++
		}
		if s.Check.AMP {
			n++
			if s.AMP == "" {
				return nil, fmt.Errorf("service %s: an amp check needs amp: <instance name>", s.ID)
			}
		}
		if s.Check.Pipeline {
			n++
			if c.Pipeline == nil {
				return nil, fmt.Errorf("service %s: a pipeline check needs a pipeline: section", s.ID)
			}
		}
		if s.Check.Freshness != nil {
			n++
			f := s.Check.Freshness
			if f.URL == "" || f.Field == "" || f.MaxAge == 0 {
				return nil, fmt.Errorf("service %s: freshness needs url, field and max_age", s.ID)
			}
		}
		if n != 1 {
			return nil, fmt.Errorf("service %s: give exactly one check (http, tcp, container, kuma, freshness, amp or pipeline)", s.ID)
		}
		if s.Check.Container && s.Container == "" {
			return nil, fmt.Errorf("service %s: a container check needs container: <name>", s.ID)
		}
		for _, a := range s.Actions {
			if !knownAction[a] {
				return nil, fmt.Errorf("service %s: %q isn't a T.I.T.A.N action (use check_now, start_service, restart_service, run_sync, recent_problems)", s.ID, a)
			}
			if (a == ActStart || a == ActRestart) && s.Container == "" && s.AMP == "" {
				return nil, fmt.Errorf("service %s: %s needs container: <name> or amp: <instance>", s.ID, a)
			}
			if a == ActLogs && s.Container == "" {
				return nil, fmt.Errorf("service %s: %s needs container: <name>", s.ID, a)
			}
			if a == ActSync && s.Sync == "" {
				return nil, fmt.Errorf("service %s: run_sync needs sync: <job name>", s.ID)
			}
		}
		for i, in := range s.Info {
			k := 0
			for _, src := range []*Source{in.Tautulli, in.Seerr, in.Arr, in.Argos} {
				if src != nil {
					k++
					if src.URL == "" {
						return nil, fmt.Errorf("service %s: info[%d] needs a url", s.ID, i)
					}
				}
			}
			if in.JSON != nil {
				k++
				if in.JSON.URL == "" || in.JSON.Field == "" || in.JSON.Label == "" {
					return nil, fmt.Errorf("service %s: info[%d] json needs url, field and label", s.ID, i)
				}
			}
			if k != 1 {
				return nil, fmt.Errorf("service %s: info[%d] needs exactly one of tautulli, seerr, arr, argos or json", s.ID, i)
			}
		}
	}
	for _, s := range c.Services {
		for _, d := range s.DependsOn {
			if c.Find(d) == nil {
				return nil, fmt.Errorf("service %s: depends_on %q isn't a listed service", s.ID, d)
			}
		}
		if c.cycle(s.ID, map[string]bool{}) {
			return nil, fmt.Errorf("service %s: depends_on goes round in a circle", s.ID)
		}
	}
	return &c, nil
}

func (c *Config) cycle(id string, path map[string]bool) bool {
	if path[id] {
		return true
	}
	path[id] = true
	defer delete(path, id)
	for _, d := range c.Find(id).DependsOn {
		if c.cycle(d, path) {
			return true
		}
	}
	return false
}

// Find returns the service with this id.
func (c *Config) Find(id string) *Service {
	for i := range c.Services {
		if c.Services[i].ID == id {
			return &c.Services[i]
		}
	}
	return nil
}

// Visible reports whether an account (role, name) may see s.
func (s *Service) Visible(role, name string) bool {
	if role == Owner {
		return true
	}
	if s.Person != "" {
		return s.Person == name
	}
	return AtLeast(role, s.Audience)
}

// Allows reports whether an account may ask for action a on s.
func (s *Service) Allows(role, name, a string) bool {
	if !s.Visible(role, name) {
		return false
	}
	has := a == ActCheck // everyone who sees a service may re-check it
	for _, x := range s.Actions {
		if x == a {
			has = true
		}
	}
	if !has {
		return false
	}
	if a == ActCheck {
		return true
	}
	if s.Person != "" && s.Person == name {
		return true // your own things are yours to fix
	}
	return AtLeast(role, s.FixBy)
}
