package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// maxPaneRows is the most content lines a pane shows, under its header.
const maxPaneRows = 8

// pane is a toggled panel drawn above the composer (DESIGN §10). rows gives its content, at most n lines, from the view's state;
// the toggle command has its name.
type pane struct {
	name string
	help string
	rows func(a *App, n int) []string
}

// panes are the screen's panes. One shows at a time; its command toggles it, and another's replaces it.
func panes() []pane {
	return []pane{
		{"todos", "the model's task list", func(a *App, n int) []string { return todoRows(a.Screen.Todos(), n) }},
	}
}

// togglePane shows the named pane, or hides it when it is the one showing.
func (a *App) togglePane(name string) {
	if a.pane == name {
		a.pane = ""
		return
	}
	a.pane = name
}

// paneCommands are the commands that toggle each pane, for the command list.
func paneCommands() []command {
	var out []command
	for _, p := range panes() {
		name := p.name
		out = append(out, command{name, "show or hide the " + name + " pane: " + p.help, func(a *App, _ string) tea.Cmd {
			a.togglePane(name)
			return nil
		}})
	}
	return out
}

// paneRows draws the showing pane: a header and its content, in at most room lines. A pane needs room for its header and one
// line, or it is not drawn, so a small terminal keeps its transcript.
func (a *App) paneRows(room int) []string {
	if a.pane == "" {
		return nil
	}
	room = min(room, 1+maxPaneRows)
	if room < 2 {
		return nil
	}
	for _, p := range panes() {
		if p.name != a.pane {
			continue
		}
		head := "── " + p.name + " "
		if n := a.width - len([]rune(head)); n > 0 {
			head += strings.Repeat("─", n)
		}
		out := []string{fit(head, a.width)}
		for _, l := range p.rows(a, room-1) {
			out = append(out, fit(l, a.width))
		}
		return out
	}
	return nil
}
