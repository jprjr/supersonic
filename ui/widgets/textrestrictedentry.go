package widgets

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// A widget.Entry that allows restrictions on the text
// that can be typed into it, based on a charAllowed callback.
type TextRestrictedEntry struct {
	widget.Entry

	charAllowed func(string, rune) bool

	minWidth float32
}

func NewTextRestrictedEntry(charAllowed func(curText string, r rune) bool) *TextRestrictedEntry {
	e := &TextRestrictedEntry{charAllowed: charAllowed}
	e.ExtendBaseWidget(e)
	return e
}

func (e *TextRestrictedEntry) TypedRune(r rune) {
	if e.charAllowed == nil || e.charAllowed(e.Text, r) {
		e.Entry.TypedRune(r)
	}
}

func (e *TextRestrictedEntry) SetMinCharWidth(numChars int) {
	e.minWidth = theme.Padding()*2 + fyne.MeasureText(strings.Repeat("W", numChars),
		fyne.CurrentApp().Settings().Theme().Size(theme.SizeNameText), e.TextStyle).Width
}

func (e *TextRestrictedEntry) MinSize() fyne.Size {
	if e.minWidth < 0.001 {
		return e.Entry.MinSize()
	}
	return fyne.NewSize(e.minWidth, e.Entry.MinSize().Height)
}
