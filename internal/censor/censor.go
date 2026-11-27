// Package censor masks swearing and slurs in the room chat: "f***". It
// works on words, after undoing the usual dodges (l33t letters, stretched
// letters like "fuuuck", and spaced or dotted letters like "f.u.c.k").
//
// A word list can't catch everything, so the owner can also remove a line
// and add words of their own (room.words in titan.yaml).
package censor

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// anywhere are roots that are bad inside any word ("bullshit", "fucking").
var anywhere = []string{
	"fuck", "fuk", "fck", "shit", "cunt", "nigger", "nigga", "niga", "faggot", "fagot", "motherf",
	"kike", "wetback", "sandnig", "towelhead", "raghead",
}

// whole are bad as a whole word, or with a common ending ("bitches").
// They are kept out of "anywhere" because they hide inside innocent words
// ("class", "cocktail", "Dickens", "spicy", "therapist").
var whole = []string{
	"ass", "asshole", "arse", "arsehole", "bitch", "bastard", "dick", "dickhead", "cock", "cocksucker",
	"pussy", "whore", "slut", "twat", "wanker", "prick", "bollocks", "piss", "jackass", "dumbass",
	"fag", "dyke", "tranny", "retard", "chink", "gook", "spic", "spick", "beaner", "coon", "paki",
	"jap", "wop", "dago", "kyke", "gyp", "cracker", "honky", "nazi",
}

var endings = []string{"", "s", "es", "ed", "er", "ers", "ing", "head", "heads", "face", "hole", "holes"}

var leet = strings.NewReplacer("0", "o", "1", "i", "!", "i", "3", "e", "4", "a", "@", "a", "5", "s", "$", "s", "7", "t", "+", "t", "8", "b", "9", "g")

// Filter is one word list: the built-in one plus the owner's extra words.
type Filter struct {
	anywhere, whole map[string]bool
}

// New builds a filter. extra words count as whole words.
func New(extra []string) *Filter {
	f := &Filter{anywhere: map[string]bool{}, whole: map[string]bool{}}
	for _, w := range anywhere {
		f.anywhere[squash(w)] = true
	}
	for _, w := range append(append([]string{}, whole...), extra...) {
		if w = squash(norm(w)); w != "" {
			f.whole[w] = true
		}
	}
	return f
}

// norm lowercases and undoes l33t letters.
func norm(s string) string { return leet.Replace(strings.ToLower(s)) }

// squash folds stretched letters: "fuuuuck" and "fuck" both become "fuck".
func squash(s string) string {
	var b strings.Builder
	var last rune
	for _, r := range s {
		if r != last {
			b.WriteRune(r)
		}
		last = r
	}
	return b.String()
}

// Bad reports whether one word is on the list.
func (f *Filter) Bad(word string) bool {
	w := squash(norm(word))
	w = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) {
			return r
		}
		return -1
	}, w)
	if w == "" {
		return false
	}
	for root := range f.anywhere {
		if strings.Contains(w, root) {
			return true
		}
	}
	for _, e := range endings {
		if base, ok := strings.CutSuffix(w, squash(e)); ok && f.whole[base] {
			return true
		}
	}
	return false
}

type span struct{ from, to int } // byte offsets into the text

// wordy is what can be part of a word, including the l33t stand-ins.
func wordy(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("@$!+*", r)
}

func words(s string) []span {
	var out []span
	start := -1
	for i, r := range s {
		if wordy(r) {
			if start < 0 {
				start = i
			}
		} else if start >= 0 {
			out = append(out, span{start, i})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, span{start, len(s)})
	}
	return out
}

// Clean returns the text with bad words masked, and whether it changed.
func (f *Filter) Clean(s string) (string, bool) {
	ws := words(s)
	var bad []span
	for i := 0; i < len(ws); i++ {
		w := ws[i]
		// Spaced-out letters: f u c k, f.u.c.k, f-u-c-k.
		if utf8.RuneCountInString(s[w.from:w.to]) == 1 {
			j := i
			var joined strings.Builder
			for j < len(ws) && utf8.RuneCountInString(s[ws[j].from:ws[j].to]) == 1 && (j == i || ws[j].from-ws[j-1].to <= 2) {
				joined.WriteString(s[ws[j].from:ws[j].to])
				j++
			}
			if j-i >= 3 && f.Bad(joined.String()) {
				bad = append(bad, span{w.from, ws[j-1].to})
				i = j - 1
				continue
			}
		}
		if f.Bad(s[w.from:w.to]) {
			bad = append(bad, w)
		}
	}
	if len(bad) == 0 {
		return s, false
	}
	var b strings.Builder
	at := 0
	for _, sp := range bad {
		b.WriteString(s[at:sp.from])
		first := true
		for _, r := range s[sp.from:sp.to] {
			switch {
			case first:
				b.WriteRune(r)
				first = false
			case wordy(r):
				b.WriteByte('*')
			default:
				// keep the dots and spaces in "f.u.c.k" so the line keeps its shape
				b.WriteRune(r)
			}
		}
		at = sp.to
	}
	b.WriteString(s[at:])
	return b.String(), true
}
