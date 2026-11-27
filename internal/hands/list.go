package hands

// list is the read-only inventory: every container's name, image, state,
// health, published ports and a few known labels. It never returns env,
// mounts or other labels, which can hold secrets. Turn it off with
// TITAN_HANDS_DISCOVER=0.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const OpList = "list"

// Container is one entry of the inventory.
type Container struct {
	Name   string            `json:"name"`
	Image  string            `json:"image"`
	State  string            `json:"state"`  // running, exited, ...
	Status string            `json:"status"` // Docker's words: "Up 3 hours (healthy)"
	Health string            `json:"health,omitempty"`
	Ports  []int             `json:"ports,omitempty"` // published host ports
	Labels map[string]string `json:"labels,omitempty"`
}

// Only these labels leave titan-hands.
func keepLabel(k string) bool {
	return strings.HasPrefix(k, "titan.") || k == "net.unraid.docker.webui" || k == "net.unraid.docker.icon" ||
		k == "org.opencontainers.image.title" || k == "com.docker.compose.project"
}

func (s *Server) list(ctx context.Context) (Reply, int) {
	if s.Rules.NoDiscover {
		return Reply{Message: "Listing containers is turned off in titan-hands (TITAN_HANDS_DISCOVER=0)."}, 403
	}
	code, body, err := s.docker(ctx, "GET", "/containers/json?all=1")
	if err != nil {
		return Reply{Message: err.Error()}, 502
	}
	if code != 200 {
		return Reply{Message: fmt.Sprintf("Docker said %d: %s", code, dockerMsg(body))}, 502
	}
	var raw []struct {
		Names        []string
		Image, State string
		Status       string
		Labels       map[string]string
		Ports        []struct{ PublicPort int }
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Reply{Message: "Docker's container list didn't make sense: " + err.Error()}, 502
	}
	out := []Container{}
	for _, c := range raw {
		if len(c.Names) == 0 {
			continue
		}
		k := Container{Name: strings.TrimPrefix(c.Names[0], "/"), Image: c.Image, State: c.State, Status: c.Status}
		switch {
		case strings.Contains(c.Status, "(unhealthy)"):
			k.Health = "unhealthy"
		case strings.Contains(c.Status, "(healthy)"):
			k.Health = "healthy"
		case strings.Contains(c.Status, "(health: starting)"):
			k.Health = "starting"
		}
		seen := map[int]bool{}
		for _, p := range c.Ports {
			if p.PublicPort > 0 && !seen[p.PublicPort] {
				seen[p.PublicPort] = true
				k.Ports = append(k.Ports, p.PublicPort)
			}
		}
		sort.Ints(k.Ports)
		for lk, lv := range c.Labels {
			if keepLabel(lk) {
				if k.Labels == nil {
					k.Labels = map[string]string{}
				}
				k.Labels[lk] = lv
			}
		}
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return Reply{OK: true, Message: fmt.Sprintf("%d containers", len(out)), Containers: out}, 200
}

// List asks titan-hands for the inventory.
func (c *Client) List(ctx context.Context) ([]Container, error) {
	rep, err := c.Do(ctx, OpList, "")
	if err != nil {
		return nil, err
	}
	return rep.Containers, nil
}
