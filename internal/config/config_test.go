package config

import (
	"strings"
	"testing"
)

func TestConfig_Errors(t *testing.T) {
	for _, tc := range []struct{ y, want string }{
		{"services: [{id: Bad Name, check: {http: x}}]", "id must be"},
		{"services: [{id: a, audience: everyone, fix_by: friends, check: {http: x}, actions: [rm_rf]}]", "isn't a T.I.T.A.N action"},
		{"services: [{id: a, check: {http: x}, actions: [restart_service]}]", "needs container"},
		{"services: [{id: a, check: {}}]", "exactly one check"},
		{"services: [{id: a, audience: family, fix_by: friends, check: {http: x}}]", "wider than audience"},
		{"surprise: 1", "field surprise not found"},
	} {
		if _, err := Parse([]byte(tc.y)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v", tc.y, err)
		}
	}
}

func TestConfig_Allows(t *testing.T) {
	c, err := Parse([]byte(`
services:
  - {id: mc, audience: friends, container: mc, check: {container: true}, actions: [restart_service]}
  - {id: faith, audience: family, fix_by: owner, container: faith, check: {container: true}, actions: [restart_service]}
  - {id: mine, audience: family, person: sam, container: h, check: {container: true}, actions: [restart_service]}
`))
	if err != nil {
		t.Fatal(err)
	}
	mc, faith, mine := c.Find("mc"), c.Find("faith"), c.Find("mine")
	for _, tc := range []struct {
		s          *Service
		role, name string
		act        string
		want       bool
	}{
		{mc, Friend, "alex", ActRestart, true},
		{mc, Friend, "alex", ActStart, false}, // not listed
		{faith, Friend, "alex", ActCheck, false},
		{faith, Family, "kim", ActCheck, true},
		{faith, Family, "kim", ActRestart, false}, // fix_by owner
		{faith, Owner, "wizard", ActRestart, true},
		{mine, Family, "kim", ActCheck, false},
		{mine, Family, "sam", ActRestart, true},
		{mine, Owner, "wizard", ActRestart, true},
	} {
		if got := tc.s.Allows(tc.role, tc.name, tc.act); got != tc.want {
			t.Errorf("%s %s(%s) %s: got %v", tc.s.ID, tc.name, tc.role, tc.act, got)
		}
	}
}

func TestConfig_DependsOnAndInfo(t *testing.T) {
	for _, tc := range []struct{ y, want string }{
		{"services: [{id: a, check: {http: x}, depends_on: [nope]}]", "isn't a listed service"},
		{"services: [{id: a, check: {http: x}, depends_on: [b]}, {id: b, check: {http: x}, depends_on: [a]}]", "circle"},
		{"services: [{id: a, check: {http: x}, info: [{}]}]", "exactly one of"},
		{"services: [{id: a, check: {http: x}, info: [{json: {url: u}}]}]", "json needs"},
		{"services: [{id: a, check: {amp: true}}]", "needs amp:"},
		{"services: [{id: a, amp: E1, check: {amp: true}, actions: [recent_problems]}]", "needs container"},
	} {
		if _, err := Parse([]byte(tc.y)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v", tc.y, err)
		}
	}
	if _, err := Parse([]byte("services: [{id: g, amp: Enshrouded01, check: {amp: true}, actions: [start_service, restart_service]}]")); err != nil {
		t.Fatal(err)
	}
}
