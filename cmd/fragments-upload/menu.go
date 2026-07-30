package main

import (
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// sourceItem adapts a Source to bubbles/list's two-line DefaultItem: volume
// name on the first line, stats + path on the second.
type sourceItem struct {
	src Source
	now time.Time
}

func (it sourceItem) Title() string { return it.src.Volume }
func (it sourceItem) Description() string {
	return fmt.Sprintf("%s · %s · %s — %s",
		frenchCount(it.src.Files), frenchSize(it.src.Size),
		frenchAgo(it.src.LastMod, it.now), it.src.Path)
}
func (it sourceItem) FilterValue() string { return it.src.Volume }

type menuModel struct {
	list   list.Model
	choice *Source
}

func newMenuModel(sources []Source) menuModel {
	now := time.Now()
	items := make([]list.Item, len(sources))
	for i, s := range sources {
		items[i] = sourceItem{src: s, now: now}
	}
	l := list.New(items, list.NewDefaultDelegate(), 0, 0)
	l.Title = "Choisissez la source à sauvegarder"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.SetShowHelp(false) // the built-in help is English; View adds a French footer
	return menuModel{list: l}
}

func (m menuModel) Init() tea.Cmd { return nil }

func (m menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Reserve two rows for the footer.
		m.list.SetSize(msg.Width, msg.Height-2)
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			if it, ok := m.list.SelectedItem().(sourceItem); ok {
				m.choice = &it.src
			}
			return m, tea.Quit
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m menuModel) View() string {
	return m.list.View() + "\n  ↑/↓ choisir · Entrée valider · q quitter\n"
}

// pickSource shows the interactive menu and returns the selected source, or
// nil if the user backed out. The TUI renders on stderr so stdout stays clean.
func pickSource(sources []Source) (*Source, error) {
	final, err := tea.NewProgram(newMenuModel(sources),
		tea.WithOutput(os.Stderr), tea.WithAltScreen()).Run()
	if err != nil {
		return nil, err
	}
	return final.(menuModel).choice, nil
}
