package redact

import (
	"strings"
	"testing"
)

func TestRedact_Line(t *testing.T) {
	in := `connect postgres://faith:hunter22@db:5432 token=abcd1234efgh Authorization: Bearer abcdefghijklmnop1234 "api_key": "zzz999" mail sam@example.com sk-ABCDEFGHIJKLMNOPQRST`
	out := Line(in)
	for _, bad := range []string{"hunter22", "abcd1234efgh", "abcdefghijklmnop1234", "zzz999", "sam@example.com", "sk-ABCD"} {
		if strings.Contains(out, bad) {
			t.Errorf("%q left in %q", bad, out)
		}
	}
	if Line("server started on :8097") != "server started on :8097" {
		t.Error("plain lines stay as they are")
	}
}
