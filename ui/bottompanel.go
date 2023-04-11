package ui

import (
	"image"
	"supersonic/backend"
	"supersonic/ui/controller"
	"supersonic/ui/layouts"
	"supersonic/ui/widgets"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/dweymouth/go-subsonic/subsonic"
)

type BottomPanel struct {
	widget.BaseWidget

	ImageManager *backend.ImageManager

	NowPlaying  *widgets.NowPlayingCard
	Controls    *widgets.PlayerControls
	AuxControls *widgets.AuxControls
	container   *fyne.Container
}

var _ fyne.Widget = (*BottomPanel)(nil)

func NewBottomPanel(pm *backend.PlaybackManager, nav func(controller.Route)) *BottomPanel {
	bp := &BottomPanel{}
	bp.ExtendBaseWidget(bp)
	pm.OnPausedOrStopped(func() {
		bp.Controls.SetPlaying(false)
	})
	pm.OnPlaying(func() {
		bp.Controls.SetPlaying(true)
	})
	pm.OnSongChange(bp.onSongChange)
	pm.OnPlayTimeUpdate(func(cur, total float64) {
		if !pm.IsSeeking() {
			bp.Controls.UpdatePlayTime(cur, total)
		}
	})

	bp.NowPlaying = widgets.NewNowPlayingCard()
	bp.NowPlaying.OnAlbumNameTapped(func() {
		nav(controller.AlbumRoute(pm.NowPlaying().AlbumID))
	})
	bp.NowPlaying.OnArtistNameTapped(func() {
		nav(controller.ArtistRoute(pm.NowPlaying().ArtistID))
	})
	bp.Controls = widgets.NewPlayerControls()
	bp.Controls.OnPlayPause(func() {
		pm.PlayPause()
	})
	bp.Controls.OnSeekNext(func() {
		pm.SeekNext()
	})
	bp.Controls.OnSeekPrevious(func() {
		pm.SeekBackOrPrevious()
	})
	bp.Controls.OnSeek(func(f float64) {
		pm.SeekFraction(f)
	})

	bp.AuxControls = widgets.NewAuxControls(pm.GetVolume())
	bp.AuxControls.VolumeControl.OnVolumeChanged = func(v int) {
		pm.SetVolume(v)
	}

	bp.container = container.New(layouts.NewLeftMiddleRightLayout(500),
		bp.NowPlaying, bp.Controls, bp.AuxControls)
	return bp
}

func (bp *BottomPanel) onSongChange(song *subsonic.Child, _ *subsonic.Child) {
	if song == nil {
		bp.NowPlaying.Update("", "", "", nil)
	} else {
		var im image.Image
		if bp.ImageManager != nil {
			// set image to expire not long after the length of the song
			// if song is played through without much pausing, image will still
			// be in cache for the next song if it's from the same album, or
			// if the user navigates to the album page for the track
			imgTTLSec := song.Duration + 30
			im, _ = bp.ImageManager.GetAlbumThumbnailWithTTL(song.CoverArt, time.Duration(imgTTLSec)*time.Second)
		}
		bp.NowPlaying.Update(song.Title, song.Artist, song.Album, im)
	}
}

func (bp *BottomPanel) CreateRenderer() fyne.WidgetRenderer {
	bp.ExtendBaseWidget(bp)
	return widget.NewSimpleRenderer(bp.container)
}
