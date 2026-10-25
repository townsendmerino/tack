package pull

import "testing"

func TestSelect(t *testing.T) {
	if Select(Model{n: 1}) != 3 {
		t.Fatal("select")
	}
}

func TestSize(t *testing.T) {
	t.Run("sub", func(t *testing.T) {
		_ = Model{}.Size()
	})
}
