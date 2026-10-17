package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// SmokeGate reads an E0 run's results.jsonl and reports whether arm's smoke met E0's rule: at least 17 of 20 on both
// runs, with 0 malformed (docs/measurements/e0-preregistration-2026-10-16.md, "Decision rule"). It returns the score
// when it did, and an error saying why not otherwise. A missing file, a missing arm and a missing smoke summary are each
// a refusal: the gate never passes on the absence of a result. With several summaries for the arm, the last one counts.
func SmokeGate(results, arm string) (string, error) {
	f, err := os.Open(results)
	if err != nil {
		return "", fmt.Errorf("no E0 results to gate on: %w", err)
	}
	defer f.Close()
	var last *struct {
		Pass      []int `json:"pass"`
		Malformed int   `json:"malformed"`
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var rec struct {
			Arm, Phase string
			Pass       []int `json:"pass"`
			Malformed  int   `json:"malformed"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Arm != arm || rec.Phase != "smoke_summary" {
			continue
		}
		last = &struct {
			Pass      []int `json:"pass"`
			Malformed int   `json:"malformed"`
		}{rec.Pass, rec.Malformed}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("reading %s: %w", results, err)
	}
	if last == nil {
		return "", fmt.Errorf("%s has no smoke summary for arm %s: its smoke did not run or did not finish", results, arm)
	}
	if len(last.Pass) != 2 {
		return "", fmt.Errorf("arm %s's smoke summary has %d runs, want 2", arm, len(last.Pass))
	}
	score := fmt.Sprintf("smoke %d/%d of %d, malformed %d", last.Pass[0], last.Pass[1], len(SmokeTasks), last.Malformed)
	if min(last.Pass[0], last.Pass[1]) < 17 || last.Malformed != 0 {
		return "", fmt.Errorf("arm %s did not meet E0's smoke rule (at least 17 of 20 on both runs, 0 malformed): %s", arm, score)
	}
	return score, nil
}
