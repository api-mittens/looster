package behaviour

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Manifest is an immutable complete configuration version. Source retains the
// original file byte for byte, including comments and formatting.
type Manifest struct {
	ID        int64     `json:"id"`
	Source    string    `json:"source"`
	Rules     []Rule    `json:"rules"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

func quote(s string) string { data, _ := json.Marshal(s); return string(data) }

// Format generates a migration source when no original file is available.
func Format(rules []Rule) string {
	var b strings.Builder
	for _, r := range rules {
		switch r.Trigger {
		case "agent", "prompt":
			fmt.Fprintf(&b, "AGENT %s WITH %s", r.Target, quote(r.Prompt))
		case "tag":
			fmt.Fprintf(&b, "TAG %s WITH %s", r.Target, quote(r.Prompt))
		case "every":
			fmt.Fprintf(&b, "EVERY %d SECONDS PING %s WITH %s", r.IntervalSeconds, r.Target, quote(r.Prompt))
		case "completed":
			fmt.Fprintf(&b, "AFTER %s COMPLETED PING %s", r.Source, r.Target)
		default:
			if strings.HasPrefix(r.Trigger, "stop_") {
				fmt.Fprintf(&b, "AFTER %s STOP PINGING %s", strings.ToUpper(strings.TrimPrefix(r.Trigger, "stop_")), r.Target)
			} else {
				fmt.Fprintf(&b, "ON %s PING %s", strings.ToUpper(r.Trigger), r.Target)
			}
		}
		if r.TagPattern != "" {
			b.WriteString(" WHEN ")
			if r.TagNegated {
				b.WriteString("NOT ")
			}
			fmt.Fprintf(&b, "TAGGED %s", r.TagPattern)
		}
		b.WriteString(";\n")
	}
	return b.String()
}
