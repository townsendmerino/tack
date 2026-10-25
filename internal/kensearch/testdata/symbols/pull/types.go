package pull

// Model is a pulled model.
type Model struct{ n int }

// Select picks one.
func Select(m Model) int {
	return m.Size() + helper()
}

func helper() int { return 1 }
