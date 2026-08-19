package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	_ "image/jpeg"
	"os"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	bubblekitten "github.com/arthursfares/bubblekitten"
)

//go:embed kenobi.jpeg
var exampleJPEG []byte

const imgCols, imgRows = 60, 17

type model struct {
	img       bubblekitten.Model
	initCmd   tea.Cmd
	altScreen bool
}

func initialModel() model {
	img, cmd := bubblekitten.New().SetImage(loadExampleImage())
	return model{img: img, initCmd: cmd}
}

func (m model) Init() tea.Cmd {
	return m.initCmd
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		var cmd tea.Cmd
		m.img, cmd = m.img.SetSize(imgCols, imgRows)
		cmds = append(cmds, cmd)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			cmds = append(cmds, m.img.Close(), tea.Quit)
			return m, tea.Sequence(cmds...)
		case "f":
			m.altScreen = !m.altScreen
			var cmd tea.Cmd
			m.img, cmd = m.img.SetAltScreen(m.altScreen)
			cmds = append(cmds, cmd)
		}
	case bubblekitten.ErrMsg:
		fmt.Fprintln(os.Stderr, "image error:", msg.Err)
	}

	var cmd tea.Cmd
	m.img, cmd = m.img.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m model) View() tea.View {
	help := lipgloss.NewStyle().Faint(true).Render("f: toggle AltScreen  •  q: quit")
	content := lipgloss.JoinVertical(lipgloss.Left, m.img.View(), "", help)
	v := tea.NewView(content)
	v.AltScreen = m.altScreen
	return v
}

func loadExampleImage() image.Image {
	img, _, err := image.Decode(bytes.NewReader(exampleJPEG))
	if err != nil { panic(err) }
	return img
}

func main() {
	if _, err := tea.NewProgram(initialModel()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
