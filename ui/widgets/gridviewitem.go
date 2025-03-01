package widgets

import (
	"context"
	"image"

	"supersonic/res"
	"supersonic/ui/layouts"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

var _ fyne.Widget = (*GridViewItem)(nil)

var _ fyne.Widget = (*coverImage)(nil)
var _ fyne.Tappable = (*coverImage)(nil)
var _ fyne.SecondaryTappable = (*coverImage)(nil)

type coverImage struct {
	widget.BaseWidget

	Im                *canvas.Image
	playbtn           *canvas.Image
	OnPlay            func()
	OnShowPage        func()
	OnShowContextMenu func(fyne.Position)
}

func newCoverImage() *coverImage {
	c := &coverImage{}
	c.ExtendBaseWidget(c)
	c.Im = &canvas.Image{FillMode: canvas.ImageFillContain, ScaleMode: canvas.ImageScaleFastest}
	c.Im.SetMinSize(fyne.NewSize(200, 200))
	c.playbtn = &canvas.Image{FillMode: canvas.ImageFillContain, Resource: res.ResPlaybuttonPng}
	c.playbtn.SetMinSize(fyne.NewSize(60, 60))
	c.playbtn.Hidden = true
	return c
}

func (c *coverImage) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(
		container.NewMax(c.Im, container.NewCenter(c.playbtn)),
	)
}

func (c *coverImage) Cursor() desktop.Cursor {
	return desktop.PointerCursor
}

func (c *coverImage) Tapped(e *fyne.PointEvent) {
	if isInside(c.center(), c.playbtn.Size().Height/2, e.Position) {
		if c.OnPlay != nil {
			c.OnPlay()
		}
		return
	}
	if c.OnShowPage != nil {
		c.OnShowPage()
	}
}

func (c *coverImage) TappedSecondary(e *fyne.PointEvent) {
	if c.OnShowContextMenu != nil {
		c.OnShowContextMenu(e.AbsolutePosition)
	}
}

func (a *coverImage) MouseIn(*desktop.MouseEvent) {
	a.playbtn.Hidden = false
	a.Refresh()
}

func (a *coverImage) MouseOut() {
	a.playbtn.Hidden = true
	a.Refresh()
}

func (a *coverImage) MouseMoved(e *desktop.MouseEvent) {
	if isInside(a.center(), a.playbtn.MinSize().Height/2, e.Position) {
		a.playbtn.SetMinSize(fyne.NewSize(65, 65))
	} else {
		a.playbtn.SetMinSize(fyne.NewSize(60, 60))
	}
	a.Refresh()
}

func (a *coverImage) center() fyne.Position {
	return fyne.NewPos(a.Size().Width/2, a.Size().Height/2)
}

func (a *coverImage) SetImage(im image.Image) {
	a.Im.Resource = nil
	a.Im.Image = im
	a.Refresh()
}

func (a *coverImage) SetImageResource(res *fyne.StaticResource) {
	a.Im.Image = nil
	a.Im.Resource = res
	a.Refresh()
}

func isInside(origin fyne.Position, radius float32, point fyne.Position) bool {
	x, y := (point.X - origin.X), (point.Y - origin.Y)
	return x*x+y*y <= radius*radius
}

type GridViewItemModel struct {
	Name        string
	ID          string
	CoverArtID  string
	Secondary   string
	SecondaryID string
}

type GridViewItem struct {
	widget.BaseWidget

	itemID        string
	secondaryID   string
	primaryText   *CustomHyperlink
	secondaryText *CustomHyperlink
	menu          *widget.PopUpMenu
	container     *fyne.Container

	// updated by GridView
	Cover *coverImage

	// these fields are used by GridView to track async update tasks
	PrevID        string
	ImgLoadCancel context.CancelFunc

	OnPlay              func(shuffle bool)
	OnAddToQueue        func()
	OnAddToPlaylist     func()
	OnShowItemPage      func()
	OnShowSecondaryPage func()
}

func NewGridViewItem() *GridViewItem {
	g := &GridViewItem{
		primaryText:   NewCustomHyperlink(),
		secondaryText: NewCustomHyperlink(),
		Cover:         newCoverImage(),
	}
	g.ExtendBaseWidget(g)
	g.Cover.OnPlay = func() { g.onPlay(false) }
	g.Cover.OnShowContextMenu = g.showContextMenu
	showItemFn := func() {
		if g.OnShowItemPage != nil {
			g.OnShowItemPage()
		}
	}
	g.Cover.OnShowPage = showItemFn
	g.primaryText.OnTapped = showItemFn
	g.secondaryText.OnTapped = func() {
		if g.OnShowSecondaryPage != nil {
			g.OnShowSecondaryPage()
		}
	}

	g.createContainer()
	return g
}

func (g *GridViewItem) createContainer() {
	info := container.New(&layouts.VboxCustomPadding{ExtraPad: -16}, g.primaryText, g.secondaryText)
	c := container.New(&layouts.VboxCustomPadding{ExtraPad: -5}, g.Cover, info)
	pad := &layouts.CenterPadLayout{PadLeftRight: 20, PadTopBottom: 10}
	g.container = container.New(pad, c)
}

func (g *GridViewItem) showContextMenu(pos fyne.Position) {
	if g.menu == nil {
		g.menu = widget.NewPopUpMenu(fyne.NewMenu("",
			fyne.NewMenuItem("Play", func() { g.onPlay(false) }),
			fyne.NewMenuItem("Shuffle", func() { g.onPlay(true) }),
			fyne.NewMenuItem("Add to queue", g.onAddToQueue),
			fyne.NewMenuItem("Add to playlist...", g.onAddToPlaylist)),
			fyne.CurrentApp().Driver().CanvasForObject(g))
	}
	g.menu.ShowAtPosition(pos)
}

func (g *GridViewItem) Update(model GridViewItemModel) {
	g.itemID = model.ID
	g.secondaryID = model.SecondaryID
	g.primaryText.SetText(model.Name)
	g.secondaryText.SetText(model.Secondary)
	g.secondaryText.Disabled = model.SecondaryID == ""
	g.secondaryText.Refresh()
	g.Cover.playbtn.Hidden = true
}

func (g *GridViewItem) ItemID() string {
	return g.itemID
}

func (g *GridViewItem) SecondaryID() string {
	return g.secondaryID
}

func (g *GridViewItem) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(g.container)
}

func (g *GridViewItem) onPlay(shuffle bool) {
	if g.OnPlay != nil {
		g.OnPlay(shuffle)
	}
}

func (g *GridViewItem) onAddToQueue() {
	if g.OnAddToQueue != nil {
		g.OnAddToQueue()
	}
}

func (g *GridViewItem) onAddToPlaylist() {
	if g.OnAddToPlaylist != nil {
		g.OnAddToPlaylist()
	}
}
