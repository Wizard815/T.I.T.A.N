package censor

import "testing"

func TestTitan_CensorMasksSwearsAndSlurs(t *testing.T) {
	f := New([]string{"grobble"})
	cases := map[string]string{
		"what the fuck is this":    "what the f*** is this",
		"FUUUCKING server":         "F******** server",
		"this is bullshit":         "this is b*******",
		"you f.u.c.k":              "you f.*.*.*",
		"sh1t happens":             "s*** happens",
		"what a b!tch":             "what a b****",
		"bitches":                  "b******",
		"grobble off":              "g****** off",
		"the minecraft server":     "the minecraft server",
		"class assignment":         "class assignment",
		"a cocktail with Dickens":  "a cocktail with Dickens",
		"spicy therapist Scotland": "spicy therapist Scotland",
		"hello":                    "hello",
	}
	for in, want := range cases {
		if got, _ := f.Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
	if _, changed := f.Clean("all good here"); changed {
		t.Error("a clean line was marked as changed")
	}
	// Slurs are caught too (checked through Bad so the test file stays short of them).
	for _, w := range []string{"n1gger", "f4ggot", "retards", "chink"} {
		if !f.Bad(w) {
			t.Errorf("Bad(%q) = false", w)
		}
	}
}
