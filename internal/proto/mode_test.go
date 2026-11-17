package proto

import "testing"

// TestStricterIsTheModeThatLetsLess: it is the lower in plan < ask < edits < auto, in either order, and a value that is not a
// mode never raises anything.
func TestStricterIsTheModeThatLetsLess(t *testing.T) {
	order := []Mode{ModePlan, ModeAsk, ModeEdits, ModeAuto}
	for i, a := range order {
		for j, b := range order {
			want := order[min(i, j)]
			if got := Stricter(a, b); got != want {
				t.Errorf("Stricter(%s, %s) = %s, want %s", a, b, got, want)
			}
		}
		if got := Stricter(a, "yolo"); got != a {
			t.Errorf("Stricter(%s, yolo) = %s", a, got)
		}
		if got := Stricter("yolo", a); got != a {
			t.Errorf("Stricter(yolo, %s) = %s", a, got)
		}
	}
	if Mode("").Valid() || Mode("yolo").Valid() {
		t.Error("a non-mode is valid")
	}
}
