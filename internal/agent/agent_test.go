package agent

import "testing"

func TestAgent_Screen(t *testing.T) {
	for _, s := range []string{
		"delete / to free space",
		"Please wipe the array, it's full",
		"run rm -rf /mnt/user",
		"format the drive",
		"remove all the files in the share",
		"can you ssh into the box and run a command",
		"delete the backups so there's room",
	} {
		if !screen.MatchString(s) {
			t.Errorf("screen should catch %q", s)
		}
	}
	for _, s := range []string{
		"why did my minecraft world get deleted?",
		"restart faith please",
		"the dnd sync is stale",
		"is plex down?",
		"how much disk space is free?",
	} {
		if screen.MatchString(s) {
			t.Errorf("screen shouldn't catch %q", s)
		}
	}
}

func TestAgent_ParseVerdict(t *testing.T) {
	for in, want := range map[string]string{
		`{"verdict":"allow","reason":"fine"}`:            Allow,
		"Sure!\n```json\n{\"verdict\": \"REFUSE\"}\n```": Refuse,
		`{"verdict":"yes"}`:                              AskOwner,
		`no json here`:                                   AskOwner,
	} {
		if got := parseVerdict(in).Verdict; got != want {
			t.Errorf("%q: got %s want %s", in, got, want)
		}
	}
}
