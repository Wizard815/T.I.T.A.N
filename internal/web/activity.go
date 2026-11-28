package web

// Activity is the panel next to the server view: what needs the owner and
// what T.I.T.A.N did (updates on the services this person can see).

import (
	"net/http"
	"sort"

	"github.com/wizard815/titan/internal/config"
)

type ActivityItem struct {
	At      int64  `json:"at"`
	Service string `json:"service,omitempty"`
	Name    string `json:"name"`
	Kind    string `json:"kind"` // needs_you | titan | person
	Who     string `json:"who,omitempty"`
	Text    string `json:"text"`
}

func (a *App) activity(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	ctx := r.Context()
	items := []ActivityItem{}
	if u.Role == config.Owner {
		notes, err := a.DB.Notes(ctx)
		if err == nil {
			for _, n := range notes {
				if len(items) >= 8 {
					break
				}
				items = append(items, ActivityItem{At: n.At, Name: n.Name, Kind: "needs_you", Text: n.Kind + ": " + n.Text})
			}
		}
	}
	ups, err := a.DB.RecentUpdates(ctx, 80)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	n := 0
	for _, up := range ups {
		s, ok := a.find(up.Service)
		if !ok || !a.sees(s, u) {
			continue
		}
		it := ActivityItem{At: up.At, Service: s.ID, Name: s.Name, Kind: "person", Who: up.Author, Text: up.Text}
		if up.Bot {
			it.Kind, it.Who = "titan", "T.I.T.A.N"
		}
		items = append(items, it)
		if n++; n >= 20 {
			break
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].At > items[j].At })
	if len(items) > 24 {
		items = items[:24]
	}
	out := map[string]any{"items": items}
	writeJSON(w, 200, out)
}
