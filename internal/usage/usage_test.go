package usage

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/townsendmerino/tack/internal/proto"
)

func ip(n int) *int { return &n }

func ev(t *testing.T, at time.Time, u proto.Usage) proto.Event {
	t.Helper()
	e, err := proto.NewEvent(1, at, proto.EvUsage, 0, u)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// TestTableSumsByDayAndServer: requests sum by day and server, a session counts once per row, reuse sums only the
// requests that reported it, prefill time likewise, and events before since are left out.
func TestTableSumsByDayAndServer(t *testing.T) {
	d1 := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	d2 := d1.AddDate(0, 0, 1)
	other, _ := proto.NewEvent(2, d1, proto.EvUserMessage, 0, proto.UserMessage{Content: []proto.ContentBlock{proto.Text("hi")}})
	logs := map[proto.SessionID][]proto.Event{
		"s1": {
			ev(t, d1.AddDate(0, 0, -3), proto.Usage{Server: "goinfer@nobara", Prompt: 999, Output: 9, WallMS: 9}),
			ev(t, d1, proto.Usage{Server: "goinfer@nobara", Prompt: 1000, Reused: ip(800), Prefilled: ip(200), Output: 50, PrefillMS: ip(400), WallMS: 2000}),
			ev(t, d1.Add(time.Minute), proto.Usage{Server: "goinfer@nobara", Prompt: 1200, Reused: ip(1000), Output: 20, WallMS: 1000}),
			other,
		},
		"s2": {
			ev(t, d1, proto.Usage{Server: "goinfer@nobara", Prompt: 500, Output: 10, WallMS: 500}),
			ev(t, d2, proto.Usage{Server: "ollama", Prompt: 300, Output: 30, WallMS: 300}),
		},
	}
	rows, err := Table(logs, d1.AddDate(0, 0, -1), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows %+v", rows)
	}
	r := rows[0]
	want := Row{Day: "2026-11-07", Server: "goinfer@nobara", Sessions: 2, Requests: 3, Prompt: 2700, Output: 80, Known: 2,
		KnownPrompt: 2200, Reused: 1800, Prefilled: 400, PrefillKnown: 1, PrefillMS: 400, WallMS: 3500}
	if r != want {
		t.Errorf("row\n got %+v\nwant %+v", r, want)
	}
	if s, ok := r.ReuseShare(); !ok || s < 0.818 || s > 0.819 {
		t.Errorf("reuse share %v %v, want 1800/2200 over the requests that said", s, ok)
	}
	if rows[1].Day != "2026-11-08" || rows[1].Server != "ollama" || rows[1].Known != 0 {
		t.Errorf("second row %+v", rows[1])
	}
	if _, ok := rows[1].ReuseShare(); ok {
		t.Error("a server that never reported reuse has no share")
	}
	if tot := Total(rows); tot.Requests != 4 || tot.Prompt != 3000 || tot.Sessions != 3 {
		t.Errorf("total %+v", tot)
	}
}

// TestTableGroupsByTheGivenZone: one instant falls on different days in different zones.
func TestTableGroupsByTheGivenZone(t *testing.T) {
	at := time.Date(2026, 11, 8, 2, 0, 0, 0, time.UTC) // 2026-11-07 18:00 in Los Angeles
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip("no tz database:", err)
	}
	logs := map[proto.SessionID][]proto.Event{"s": {ev(t, at, proto.Usage{Server: "x", Prompt: 1})}}
	u, _ := Table(logs, time.Time{}, time.UTC)
	l, _ := Table(logs, time.Time{}, la)
	if u[0].Day != "2026-11-08" || l[0].Day != "2026-11-07" {
		t.Errorf("utc %s, los angeles %s", u[0].Day, l[0].Day)
	}
}

func TestPrintMarksWhatIsUnknown(t *testing.T) {
	var b bytes.Buffer
	Print(&b, []Row{{Day: "2026-11-08", Server: "ollama", Sessions: 1, Requests: 2, Prompt: 10, WallMS: 1500},
		{Day: "2026-11-08", Server: "goinfer", Sessions: 1, Requests: 2, Prompt: 100, Known: 1, KnownPrompt: 50, Reused: 25, PrefillKnown: 1, PrefillMS: 1200}})
	out := b.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 5 || !strings.Contains(lines[1], "—") || !strings.Contains(lines[2], "50% (1/2)") || !strings.Contains(lines[2], "1.2") ||
		!strings.HasPrefix(lines[4], "total") {
		t.Errorf("table:\n%s", out)
	}
	b.Reset()
	if Print(&b, nil); !strings.Contains(b.String(), "(no requests in range)") {
		t.Errorf("empty table:\n%s", b.String())
	}
}
