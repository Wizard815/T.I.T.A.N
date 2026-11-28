package agent

// Triage is T.I.T.A.N looking at something that went down by itself,
// before anyone asks: is there a simple fix it may run, or is it one for
// the owner? It sees only that one service (its check, logs with secrets
// removed, and its playbook) and can only pick from the fixes it's given.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

type TriageInput struct {
	Name      string
	Detail    string   // what the check said
	Logs      []string // last log lines, secrets removed (may be empty)
	Playbooks string   // playbooks about this service (may be empty)
	Fixes     []string // the fixes T.I.T.A.N may run by itself
}

type TriageResult struct {
	Fix   string `json:"fix"` // one of Fixes, or "" for the owner
	Why   string `json:"why"`
	Model string `json:"model,omitempty"`
}

const triagePrompt = `You are T.I.T.A.N, looking at a service in %[1]s's homelab that went down by itself. Decide whether one simple fix from the list is likely to help, or whether %[1]s needs to look.

Pick a fix only when the evidence points to a stuck or stopped process: it's off, it crashed, it's hung, a health check timed out, a connection was refused after a restart, out of memory once. Pick none (fix "") when the logs show something a restart won't cure: bad config, missing or full disk, permission denied, a broken database, a failed update, missing files or mounts, a licence or login problem, or the same crash again and again. When in doubt, pick none.

Fixes you may pick: %[2]s

Answer with JSON only: {"fix": "<one of the fixes, or empty>", "why": "<one plain sentence a non-technical person understands, naming what you saw>"}`

// Triage asks the helper model (then the fallback) for a verdict.
func (a *Agent) Triage(ctx context.Context, in TriageInput) (TriageResult, error) {
	fixes := "none"
	if len(in.Fixes) > 0 {
		fixes = strings.Join(in.Fixes, ", ")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Service: %s\nCheck says: %s\n", in.Name, in.Detail)
	if len(in.Logs) > 0 {
		logs := in.Logs
		if len(logs) > 40 {
			logs = logs[len(logs)-40:]
		}
		b.WriteString("Last log lines:\n<logs>\n" + strings.Join(logs, "\n") + "\n</logs>\n")
	} else {
		b.WriteString("No logs available.\n")
	}
	if in.Playbooks != "" {
		b.WriteString("Playbook:\n" + in.Playbooks + "\n")
	}
	msgs := []Message{
		{Role: "system", Content: fmt.Sprintf(triagePrompt, a.Owner, fixes)},
		{Role: "user", Content: b.String()},
	}
	msg, model, err := a.Models.First(ctx, []string{a.Helper, a.Fallback}, msgs, nil, true)
	if err != nil {
		return TriageResult{}, err
	}
	var r TriageResult
	if m := jsonObj.FindString(msg.Content); m == "" || json.Unmarshal([]byte(m), &r) != nil {
		return TriageResult{}, fmt.Errorf("the triage answer couldn't be read")
	}
	r.Model = model
	r.Fix = strings.TrimSpace(r.Fix)
	if r.Fix != "" && !slices.Contains(in.Fixes, r.Fix) {
		r.Fix = "" // never anything it wasn't offered
	}
	if strings.TrimSpace(r.Why) == "" {
		r.Why = "No reason given."
	}
	return r, nil
}
