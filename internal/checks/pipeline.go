package checks

// The media pipeline: a request in Seerr becomes a search in Sonarr or
// Radarr, a download in SABnzbd, an import, and finally a new arrival in
// Plex (seen through Tautulli). Pipeline reads each stage, read only, and
// says whether things are moving, idle or stuck, and where.

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/wizard815/titan/internal/config"
)

type Download struct {
	Title    string `json:"title"`
	Kind     string `json:"kind"` // tv | movie
	State    string `json:"state"`
	Progress int    `json:"progress_percent"`
	ETA      string `json:"eta,omitempty"`
	Problem  string `json:"problem,omitempty"`
}

type PipelineReport struct {
	Verdict    string     `json:"verdict"` // moving | idle | stuck
	Summary    string     `json:"summary"`
	Pending    int        `json:"requests_waiting_for_approval"`
	Processing int        `json:"requests_on_the_way"`
	Downloads  []Download `json:"downloads"`
	Downloader string     `json:"downloader,omitempty"`
	Arrived    []string   `json:"recently_on_plex,omitempty"`
	Problems   []string   `json:"problems,omitempty"`
	Unreadable []string   `json:"could_not_read,omitempty"`
	Match      string     `json:"about_your_title,omitempty"`
}

// Pipeline reads every stage that is set up. title (optional) narrows the
// answer to one show or film.
func (r *Runner) Pipeline(ctx context.Context, title string) (*PipelineReport, error) {
	p := r.Cfg.Pipeline
	if p == nil {
		return nil, fmt.Errorf("the media pipeline isn't set up (pipeline: in titan.yaml)")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rep := &PipelineReport{Downloads: []Download{}}
	cant := func(stage string, err error) { rep.Unreadable = append(rep.Unreadable, stage+" ("+err.Error()+")") }

	if p.Seerr != nil {
		var v struct{ Pending, Processing int }
		if err := r.getJSON(ctx, strings.TrimRight(p.Seerr.URL, "/")+"/api/v1/request/count", key("X-Api-Key", p.Seerr.KeyEnv), &v); err != nil {
			cant("Seerr", err)
		} else {
			rep.Pending, rep.Processing = v.Pending, v.Processing
		}
	}
	for _, a := range []struct {
		src  *config.Source
		kind string
	}{{p.Sonarr, "tv"}, {p.Radarr, "movie"}} {
		if a.src == nil {
			continue
		}
		d, err := r.arrQueue(ctx, a.src, a.kind)
		if err != nil {
			cant(map[string]string{"tv": "Sonarr", "movie": "Radarr"}[a.kind], err)
			continue
		}
		rep.Downloads = append(rep.Downloads, d...)
	}
	sabActive := false
	if p.Sabnzbd != nil {
		var v struct {
			Queue struct {
				Status    string `json:"status"`
				Paused    bool   `json:"paused"`
				Speed     string `json:"speed"`
				NoOfSlots int    `json:"noofslots"`
				TimeLeft  string `json:"timeleft"`
			} `json:"queue"`
		}
		u := strings.TrimRight(p.Sabnzbd.URL, "/") + "/api?mode=queue&output=json&apikey=" + url.QueryEscape(os.Getenv(p.Sabnzbd.KeyEnv))
		if err := r.getJSON(ctx, u, nil, &v); err != nil {
			cant("SABnzbd", err)
		} else {
			q := v.Queue
			switch {
			case q.Paused || strings.EqualFold(q.Status, "Paused"):
				rep.Downloader = fmt.Sprintf("SABnzbd is paused with %d in its queue", q.NoOfSlots)
				if q.NoOfSlots > 0 {
					rep.Problems = append(rep.Problems, "SABnzbd is paused, so nothing downloads until it's resumed")
				}
			case q.NoOfSlots == 0:
				rep.Downloader = "SABnzbd has nothing to download"
			default:
				sabActive = true
				rep.Downloader = fmt.Sprintf("SABnzbd is downloading %d at %sB/s, about %s left", q.NoOfSlots, strings.TrimSpace(q.Speed), q.TimeLeft)
			}
		}
	}
	if p.Tautulli != nil {
		var v struct {
			Response struct {
				Data struct {
					RecentlyAdded []struct {
						Title            string `json:"title"`
						GrandparentTitle string `json:"grandparent_title"`
						MediaType        string `json:"media_type"`
						ParentMediaIndex any    `json:"parent_media_index"`
						MediaIndex       any    `json:"media_index"`
					} `json:"recently_added"`
				} `json:"data"`
			} `json:"response"`
		}
		u := strings.TrimRight(p.Tautulli.URL, "/") + "/api/v2?cmd=get_recently_added&count=8&apikey=" + url.QueryEscape(os.Getenv(p.Tautulli.KeyEnv))
		if err := r.getJSON(ctx, u, nil, &v); err != nil {
			cant("Plex (Tautulli)", err)
		} else {
			for _, a := range v.Response.Data.RecentlyAdded {
				t := a.Title
				if a.MediaType == "episode" && a.GrandparentTitle != "" {
					t = fmt.Sprintf("%s S%vE%v", a.GrandparentTitle, a.ParentMediaIndex, a.MediaIndex)
				}
				rep.Arrived = append(rep.Arrived, t)
			}
		}
	}

	moving := sabActive
	for _, d := range rep.Downloads {
		if d.Problem != "" {
			rep.Problems = append(rep.Problems, d.Title+": "+d.Problem)
		}
		if d.State == "downloading" {
			moving = true
		}
	}
	switch {
	case len(rep.Problems) > 0:
		rep.Verdict = "stuck"
		rep.Summary = "Stuck: " + rep.Problems[0]
		if n := len(rep.Problems); n > 1 {
			rep.Summary += fmt.Sprintf(" (and %d more)", n-1)
		}
	case moving:
		rep.Verdict = "moving"
		rep.Summary = fmt.Sprintf("Moving: %d downloading", len(rep.Downloads))
	default:
		rep.Verdict = "idle"
		rep.Summary = "Nothing downloading right now"
	}
	if rep.Pending > 0 {
		rep.Summary += fmt.Sprintf("; %d %s waiting for approval", rep.Pending, plural(rep.Pending, "request", "requests"))
	}
	if title = strings.ToLower(strings.TrimSpace(title)); title != "" {
		rep.Match = matchTitle(rep, title)
	}
	return rep, nil
}

func matchTitle(rep *PipelineReport, title string) string {
	for _, d := range rep.Downloads {
		if strings.Contains(strings.ToLower(d.Title), title) {
			if d.Problem != "" {
				return fmt.Sprintf("%s is stuck: %s", d.Title, d.Problem)
			}
			s := fmt.Sprintf("%s is %s, %d%% done", d.Title, d.State, d.Progress)
			if d.ETA != "" {
				s += ", about " + d.ETA + " left"
			}
			return s
		}
	}
	for _, a := range rep.Arrived {
		if strings.Contains(strings.ToLower(a), title) {
			return a + " is already on Plex"
		}
	}
	return "It isn't downloading and hasn't arrived on Plex recently. It may still be waiting for approval in Seerr, not be released yet, or not have been found."
}

func (r *Runner) arrQueue(ctx context.Context, src *config.Source, kind string) ([]Download, error) {
	inc := "includeSeries=true&includeEpisode=true"
	if kind == "movie" {
		inc = "includeMovie=true"
	}
	var v struct {
		Records []struct {
			Title, Status, TrackedDownloadStatus, TrackedDownloadState, ErrorMessage, Timeleft string
			Size, Sizeleft                                                                     float64
			StatusMessages                                                                     []struct {
				Title    string
				Messages []string
			}
			Series  *struct{ Title string }
			Episode *struct{ SeasonNumber, EpisodeNumber int }
			Movie   *struct {
				Title string
				Year  int
			}
		} `json:"records"`
	}
	if err := r.getJSON(ctx, strings.TrimRight(src.URL, "/")+"/api/v3/queue?pageSize=50&"+inc, key("X-Api-Key", src.KeyEnv), &v); err != nil {
		return nil, err
	}
	var out []Download
	for _, q := range v.Records {
		d := Download{Title: q.Title, Kind: kind, State: strings.ToLower(firstNonEmpty(q.TrackedDownloadState, q.Status))}
		switch {
		case q.Series != nil && q.Episode != nil:
			d.Title = fmt.Sprintf("%s S%dE%d", q.Series.Title, q.Episode.SeasonNumber, q.Episode.EpisodeNumber)
		case q.Movie != nil && q.Movie.Year > 0:
			d.Title = fmt.Sprintf("%s (%d)", q.Movie.Title, q.Movie.Year)
		case q.Movie != nil:
			d.Title = q.Movie.Title
		}
		if q.Size > 0 {
			d.Progress = int((q.Size - q.Sizeleft) / q.Size * 100)
		}
		if q.Timeleft != "" && q.Timeleft != "00:00:00" {
			d.ETA = q.Timeleft
		}
		bad := strings.EqualFold(q.TrackedDownloadStatus, "warning") || strings.EqualFold(q.TrackedDownloadStatus, "error") ||
			strings.EqualFold(q.Status, "failed") || strings.EqualFold(q.Status, "warning") || strings.HasPrefix(d.State, "failed")
		if bad {
			d.Problem = q.ErrorMessage
			for _, m := range q.StatusMessages {
				if d.Problem == "" && len(m.Messages) > 0 {
					d.Problem = m.Messages[0]
				}
			}
			if d.Problem == "" {
				d.Problem = "the download has a problem (" + d.State + ")"
			}
			if d.State == "importpending" || d.State == "importblocked" {
				d.Problem = "downloaded but can't be moved into the library: " + d.Problem
			}
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Problem != "" && out[j].Problem == "" })
	return out, nil
}
