package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// command is one `/` command of the composer (DESIGN §10). run gets what follows the name, trimmed, and returns what the
// program should run next.
type command struct {
	name, help string
	run        func(a *App, args string) tea.Cmd
}

// commands are the screen's own commands, in the order /help lists them. They act on the screen and the session through the
// Backend; user-defined prompt templates (DESIGN §6.7) are not here.
func (a *App) commands() []command {
	return []command{
		{"help", "list these commands", func(a *App, _ string) tea.Cmd {
			for _, c := range a.commands() {
				a.Screen.note("/" + c.name + "  " + c.help)
			}
			return nil
		}},
		{"release", "lift the hold the guard put on this session (it shows the commands that caused it)", func(a *App, _ string) tea.Cmd {
			if !a.Screen.held {
				a.Screen.note("· this session is not held")
				return nil
			}
			b := a.Backend
			return func() tea.Msg { return ReleasedMsg{Err: b.Release()} }
		}},
		{"cancel", "end the running turn (also Esc)", func(a *App, _ string) tea.Cmd { return a.cancel() }},
		{"quit", "leave the screen (also Ctrl-C)", func(a *App, _ string) tea.Cmd {
			a.quit = true
			return tea.Quit
		}},
	}
}

// slashCommand reads text as a command: a "/" and a name of lowercase letters, digits and hyphens, then nothing or a space or
// newline and arguments. A path such as /etc/hosts has a slash in its first word and is text, as is anything not starting
// with a slash.
func slashCommand(text string) (name, args string, ok bool) {
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	word, rest, _ := strings.Cut(text[1:], " ")
	if w, r, found := strings.Cut(word, "\n"); found {
		word, rest = w, r+" "+rest
	}
	if word == "" {
		return "", "", false
	}
	for i, r := range word {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' && i > 0 || r == '-' && i > 0) {
			return "", "", false
		}
	}
	return word, strings.TrimSpace(rest), true
}

// runSlash runs the command text names, and reports whether text was one. An unknown name is a note, and the caller keeps the
// text so a typo is not lost; nothing a command does is a prompt to the model.
func (a *App) runSlash(text string) (cmd tea.Cmd, handled, known bool) {
	name, args, ok := slashCommand(text)
	if !ok {
		return nil, false, false
	}
	for _, c := range a.commands() {
		if c.name == name {
			return c.run(a, args), true, true
		}
	}
	a.Screen.note("! unknown command /" + name + "; /help lists them")
	return nil, true, false
}
