package widgets

import (
	"context"
	"image"
	"log"
	"supersonic/backend"
	"supersonic/res"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
	"github.com/dweymouth/go-subsonic/subsonic"
)

const albumFetchBatchSize = 6

type ImageFetcher interface {
	GetAlbumThumbnailFromCache(string) (image.Image, bool)
	GetAlbumThumbnail(string) (image.Image, error)
}

type AlbumGrid struct {
	widget.BaseWidget

	AlbumGridState

	stateMutex   sync.RWMutex
	grid         *widget.GridWrapList
	highestShown int
	fetching     bool
}

type AlbumGridState struct {
	albums   []*subsonic.AlbumID3
	iter     *backend.BatchingIterator
	done     bool
	showYear bool

	imageFetcher     ImageFetcher
	OnPlayAlbum      func(string)
	OnShowAlbumPage  func(string)
	OnShowArtistPage func(string)

	scrollPos float32
}

var _ fyne.Widget = (*AlbumGrid)(nil)

func NewFixedAlbumGrid(albums []*subsonic.AlbumID3, fetch ImageFetcher, showYear bool) *AlbumGrid {
	ag := &AlbumGrid{
		AlbumGridState: AlbumGridState{
			albums:       albums,
			done:         true,
			imageFetcher: fetch,
			showYear:     showYear,
		},
	}
	ag.ExtendBaseWidget(ag)
	ag.createGridWrapList()
	return ag
}

func NewAlbumGrid(iter backend.AlbumIterator, fetch ImageFetcher, showYear bool) *AlbumGrid {
	ag := &AlbumGrid{
		AlbumGridState: AlbumGridState{
			iter:         backend.NewBatchingIterator(iter),
			imageFetcher: fetch,
		},
	}
	ag.ExtendBaseWidget(ag)

	ag.createGridWrapList()

	// fetch initial albums
	ag.fetchMoreAlbums(36)
	return ag
}

func (ag *AlbumGrid) SaveToState() AlbumGridState {
	ag.stateMutex.RLock()
	defer ag.stateMutex.RUnlock()
	s := ag.AlbumGridState
	s.scrollPos = ag.grid.GetScrollOffset()
	return s
}

func NewAlbumGridFromState(state AlbumGridState) *AlbumGrid {
	ag := &AlbumGrid{AlbumGridState: state}
	ag.ExtendBaseWidget(ag)
	ag.createGridWrapList()
	ag.Refresh() // needed to initialize the widget
	ag.grid.ScrollToOffset(state.scrollPos)
	return ag
}

func (ag *AlbumGrid) Clear() {
	ag.stateMutex.Lock()
	defer ag.stateMutex.Unlock()
	ag.albums = nil
	ag.done = true
}

func (ag *AlbumGrid) ResetFromState(state AlbumGridState) {
	ag.stateMutex.Lock()
	ag.AlbumGridState = state
	ag.highestShown = 0
	ag.fetching = false
	ag.stateMutex.Unlock()
	ag.grid.ScrollToOffset(state.scrollPos)
}

func (ag *AlbumGrid) Reset(iter backend.AlbumIterator) {
	ag.stateMutex.Lock()
	ag.albums = nil
	ag.fetching = false
	ag.done = false
	ag.highestShown = 0
	ag.iter = backend.NewBatchingIterator(iter)
	ag.scrollPos = 0
	ag.stateMutex.Unlock()
	ag.grid.ScrollToOffset(ag.scrollPos)
	ag.fetchMoreAlbums(36)
}

func (ag *AlbumGrid) createGridWrapList() {
	g := widget.NewGridWrapList(
		func() int {
			return ag.lenAlbums()
		},
		// create func
		func() fyne.CanvasObject {
			ac := NewAlbumCard(ag.showYear)
			ac.OnPlay = func() {
				if ag.OnPlayAlbum != nil {
					ag.OnPlayAlbum(ac.AlbumID())
				}
			}
			ac.OnShowArtistPage = func() {
				if ag.OnShowArtistPage != nil {
					ag.OnShowArtistPage(ac.ArtistID())
				}
			}
			ac.OnShowAlbumPage = func() {
				if ag.OnShowAlbumPage != nil {
					ag.OnShowAlbumPage(ac.AlbumID())
				}
			}
			return ac
		},
		// update func
		func(itemID int, obj fyne.CanvasObject) {
			ac := obj.(*AlbumCard)
			ag.doUpdateAlbumCard(itemID, ac)
		},
	)
	ag.grid = g
}

func (ag *AlbumGrid) doUpdateAlbumCard(albumIdx int, ac *AlbumCard) {
	if albumIdx > ag.highestShown {
		ag.highestShown = albumIdx
	}
	ag.stateMutex.RLock()
	album := ag.albums[albumIdx]
	ag.stateMutex.RUnlock()
	if ac.PrevAlbumID == album.ID {
		// nothing to do
		return
	}
	ac.Update(album)
	ac.PrevAlbumID = album.ID
	// cancel any previous image fetch
	if ac.ImgLoadCancel != nil {
		ac.ImgLoadCancel()
		ac.ImgLoadCancel = nil
	}
	if img, ok := ag.imageFetcher.GetAlbumThumbnailFromCache(album.CoverArt); ok {
		ac.Cover.SetImage(img)
	} else {
		ac.Cover.SetImageResource(res.ResAlbumplaceholderPng)
		// asynchronously fetch cover image
		ctx, cancel := context.WithCancel(context.Background())
		ac.ImgLoadCancel = cancel
		go func(ctx context.Context) {
			i, err := ag.imageFetcher.GetAlbumThumbnail(album.CoverArt)
			select {
			case <-ctx.Done():
				return
			default:
				if err == nil {
					ac.Cover.SetImage(i)
				} else {
					log.Printf("error fetching image: %s", err.Error())
				}
			}
		}(ctx)
	}

	// if user has scrolled near the bottom, fetch more
	if !ag.done && !ag.fetching && albumIdx > ag.lenAlbums()-10 {
		ag.fetchMoreAlbums(20)
	}
}

func (a *AlbumGrid) lenAlbums() int {
	a.stateMutex.RLock()
	defer a.stateMutex.RUnlock()
	return len(a.albums)
}

// fetches at least count more albums
func (a *AlbumGrid) fetchMoreAlbums(count int) {
	if a.iter == nil {
		a.done = true
	}
	a.fetching = true
	go func() {
		// keep repeating the fetch task as long as the user
		// has scrolled near the bottom
		for !a.done && a.highestShown >= a.lenAlbums()-10 {
			n := 0
			for !a.done && n < count {
				albums := a.iter.NextN(albumFetchBatchSize)
				a.stateMutex.Lock()
				a.albums = append(a.albums, albums...)
				a.stateMutex.Unlock()
				if len(albums) < albumFetchBatchSize {
					a.done = true
				}
				n += len(albums)
				if len(albums) > 0 {
					a.Refresh()
				}
			}
		}
		a.fetching = false
	}()
}

func (a *AlbumGrid) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(a.grid)
}
