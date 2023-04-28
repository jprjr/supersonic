package backend

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"supersonic/backend/util"
	"supersonic/player"
	"supersonic/sharedutil"
	"time"

	"github.com/dweymouth/go-subsonic/subsonic"
)

const (
	ReplayGainNone  = string(player.ReplayGainNone)
	ReplayGainAlbum = string(player.ReplayGainAlbum)
	ReplayGainTrack = string(player.ReplayGainTrack)
)

// A high-level Subsonic-aware playback backend.
// Manages loading tracks into the Player queue,
// sending callbacks on play time updates and track changes.
type PlaybackManager struct {
	ctx           context.Context
	cancelPollPos context.CancelFunc
	pollingTick   *time.Ticker
	sm            *ServerManager
	player        *player.Player

	playTimeStopwatch util.Stopwatch
	curTrackTime      float64
	callbacksDisabled bool

	playQueue     []*subsonic.Child
	nowPlayingIdx int64

	// to pass to onSongChange listeners; clear once listeners have been called
	lastScrobbled *subsonic.Child
	scrobbleCfg   *ScrobbleConfig

	onSongChange     []func(nowPlaying *subsonic.Child, justScrobbledIfAny *subsonic.Child)
	onPlayTimeUpdate []func(float64, float64)
	onPaused         []func()
	onPlaying        []func()
}

func NewPlaybackManager(
	ctx context.Context,
	s *ServerManager,
	p *player.Player,
	scrobbleCfg *ScrobbleConfig,
) *PlaybackManager {
	// clamp to 99% to avoid any possible rounding issues
	scrobbleCfg.ThresholdPercent = clamp(scrobbleCfg.ThresholdPercent, 0, 99)
	pm := &PlaybackManager{
		ctx:         ctx,
		sm:          s,
		player:      p,
		scrobbleCfg: scrobbleCfg,
	}
	p.OnTrackChange(func(tracknum int64) {
		if tracknum >= int64(len(pm.playQueue)) {
			return
		}
		pm.checkScrobble(pm.playTimeStopwatch.Elapsed())
		pm.playTimeStopwatch.Reset()
		if pm.player.GetStatus().State == player.Playing {
			pm.playTimeStopwatch.Start()
		}
		pm.nowPlayingIdx = tracknum
		pm.curTrackTime = float64(pm.playQueue[pm.nowPlayingIdx].Duration)
		pm.invokeOnSongChangeCallbacks()
		pm.doUpdateTimePos()
		pm.sendNowPlayingScrobble()
	})
	p.OnSeek(func() {
		pm.doUpdateTimePos()
	})
	p.OnStopped(func() {
		pm.playTimeStopwatch.Stop()
		pm.checkScrobble(pm.playTimeStopwatch.Elapsed())
		pm.playTimeStopwatch.Reset()
		pm.stopPollTimePos()
		pm.doUpdateTimePos()
		if !pm.callbacksDisabled {
			for _, cb := range pm.onPaused {
				cb()
			}
			pm.invokeOnSongChangeCallbacks()
		}
	})
	p.OnPaused(func() {
		pm.playTimeStopwatch.Stop()
		pm.stopPollTimePos()
		for _, cb := range pm.onPaused {
			cb()
		}
	})
	p.OnPlaying(func() {
		pm.playTimeStopwatch.Start()
		pm.startPollTimePos()
		for _, cb := range pm.onPlaying {
			cb()
		}
	})

	s.OnLogout(func() {
		pm.StopAndClearPlayQueue()
	})

	return pm
}

func (p *PlaybackManager) SetVolume(vol int) {
	p.player.SetVolume(vol)
}

func (p *PlaybackManager) GetVolume() int {
	return p.player.GetVolume()
}

// Seeks within a track by fraction [0 .. 1]
func (p *PlaybackManager) SeekFraction(f float64) {
	p.player.Seek(fmt.Sprintf("%d", int(f*100)), player.SeekAbsolutePercent)
}

func (p *PlaybackManager) IsSeeking() bool {
	return p.player.IsSeeking()
}

// Should only be called before quitting.
// Disables playback state callbacks being sent
func (p *PlaybackManager) DisableCallbacks() {
	p.callbacksDisabled = true
}

// Gets the curently playing song, if any.
func (p *PlaybackManager) NowPlaying() *subsonic.Child {
	if len(p.playQueue) == 0 || p.player.GetStatus().State == player.Stopped {
		return nil
	}
	return p.playQueue[p.nowPlayingIdx]
}

// Sets a callback that is notified whenever a new song begins playing.
func (p *PlaybackManager) OnSongChange(cb func(nowPlaying *subsonic.Child, justScrobbledIfAny *subsonic.Child)) {
	p.onSongChange = append(p.onSongChange, cb)
}

// Registers a callback that is notified whenever playback is paused or stopped.
func (p *PlaybackManager) OnPausedOrStopped(cb func()) {
	p.onPaused = append(p.onPaused, cb)
}

// Registers a callback that is notified whenever playback begins from the paused or stopped state.
func (p *PlaybackManager) OnPlaying(cb func()) {
	p.onPlaying = append(p.onPlaying, cb)
}

// Registers a callback that is notified whenever the play time should be updated.
func (p *PlaybackManager) OnPlayTimeUpdate(cb func(float64, float64)) {
	p.onPlayTimeUpdate = append(p.onPlayTimeUpdate, cb)
}

// Loads the specified album into the play queue.
func (p *PlaybackManager) LoadAlbum(albumID string, appendToQueue bool, shuffle bool) error {
	album, err := p.sm.Server.GetAlbum(albumID)
	if err != nil {
		return err
	}
	return p.LoadTracks(album.Song, appendToQueue, shuffle)
}

// Loads the specified playlist into the play queue.
func (p *PlaybackManager) LoadPlaylist(playlistID string, appendToQueue bool, shuffle bool) error {
	playlist, err := p.sm.Server.GetPlaylist(playlistID)
	if err != nil {
		return err
	}
	return p.LoadTracks(playlist.Entry, appendToQueue, shuffle)
}

func (p *PlaybackManager) LoadTracks(tracks []*subsonic.Child, appendToQueue, shuffle bool) error {
	if !appendToQueue {
		p.player.Stop()
		p.nowPlayingIdx = 0
		p.playQueue = nil
	}
	nums := util.Range(len(tracks))
	if shuffle {
		util.ShuffleSlice(nums)
	}
	for _, i := range nums {
		url, err := p.sm.Server.GetStreamURL(tracks[i].ID, map[string]string{})
		if err != nil {
			return err
		}
		p.player.AppendFile(url.String())
		// ensure a deep copy of the track info so that we can maintain our own state
		// (tracking play count increases, favorite, and rating) without messing up
		// other views' track models
		tr := *tracks[i]
		p.playQueue = append(p.playQueue, &tr)
	}
	return nil
}

func (p *PlaybackManager) PlayAlbum(albumID string, firstTrack int) error {
	if err := p.LoadAlbum(albumID, false, false); err != nil {
		return err
	}
	if firstTrack <= 0 {
		return p.player.PlayFromBeginning()
	}
	return p.player.PlayTrackAt(firstTrack)
}

func (p *PlaybackManager) PlayPlaylist(playlistID string, firstTrack int) error {
	if err := p.LoadPlaylist(playlistID, false, false); err != nil {
		return err
	}
	if firstTrack <= 0 {
		return p.player.PlayFromBeginning()
	}
	return p.player.PlayTrackAt(firstTrack)
}

func (p *PlaybackManager) PlayFromBeginning() error {
	return p.player.PlayFromBeginning()
}

func (p *PlaybackManager) PlayTrackAt(idx int) error {
	return p.player.PlayTrackAt(idx)
}

func (p *PlaybackManager) PlayRandomSongs(genreName string) {
	params := map[string]string{"size": "100"}
	if genreName != "" {
		params["genre"] = genreName
	}
	if songs, err := p.sm.Server.GetRandomSongs(params); err != nil {
		log.Printf("error getting random songs: %s", err.Error())
	} else {
		p.LoadTracks(songs, false, false)
		p.PlayFromBeginning()
	}
}

func (p *PlaybackManager) PlaySimilarSongs(id string) {
	params := map[string]string{"size": "100"}
	if songs, err := p.sm.Server.GetSimilarSongs2(id, params); err != nil {
		log.Printf("error getting similar songs: %s", err.Error())
	} else {
		p.LoadTracks(songs, false, false)
		p.PlayFromBeginning()
	}
}

func (p *PlaybackManager) GetPlayQueue() []*subsonic.Child {
	pq := make([]*subsonic.Child, len(p.playQueue))
	for i, tr := range p.playQueue {
		copy := *tr
		pq[i] = &copy
	}
	return pq
}

// Any time the user changes the favorite status of a track elsewhere in the app,
// this should be called to ensure the in-memory track model is updated.
func (p *PlaybackManager) OnTrackFavoriteStatusChanged(id string, fav bool) {
	if tr := sharedutil.FindTrackByID(id, p.playQueue); tr != nil {
		if fav {
			tr.Starred = time.Now()
		} else {
			tr.Starred = time.Time{}
		}
	}
}

// Any time the user changes the rating of a track elsewhere in the app,
// this should be called to ensure the in-memory track model is updated.
func (p *PlaybackManager) OnTrackRatingChanged(id string, rating int) {
	if tr := sharedutil.FindTrackByID(id, p.playQueue); tr != nil {
		tr.UserRating = rating
	}
}

// trackIdxs must be sorted
func (p *PlaybackManager) RemoveTracksFromQueue(trackIdxs []int) {
	newQueue := make([]*subsonic.Child, 0, len(p.playQueue)-len(trackIdxs))
	rmCount := 0
	rmIdx := 0
	for i, tr := range p.playQueue {
		if rmIdx < len(trackIdxs) && trackIdxs[rmIdx] == i {
			// removing this track
			// TODO: if we are removing the currently playing track,
			// we need to scrobble it if it played for more than the scrobble threshold
			rmIdx++
			if err := p.player.RemoveTrackAt(i - rmCount); err == nil {
				rmCount++
			} else {
				log.Printf("error removing track: %v", err.Error())
				// did not remove this track
				newQueue = append(newQueue, tr)
			}
		} else {
			// not removing this track
			newQueue = append(newQueue, tr)
		}
	}
	p.playQueue = newQueue
	p.nowPlayingIdx = p.player.GetStatus().PlaylistPos
	// fire on song change callbacks in case the playing track was removed
	// TODO: only call this if the playing track actually was removed
	p.invokeOnSongChangeCallbacks()
}

// Stop playback and clear the play queue.
func (p *PlaybackManager) StopAndClearPlayQueue() {
	p.player.Stop()
	p.player.ClearPlayQueue()
	p.doUpdateTimePos()
	p.playQueue = nil
}

func (p *PlaybackManager) PlayPause() {
	p.player.PlayPause()
}

func (p *PlaybackManager) SeekBackOrPrevious() {
	p.player.SeekBackOrPrevious()
}

func (p *PlaybackManager) SeekNext() {
	p.player.SeekNext()
}

func (p *PlaybackManager) SetReplayGainOptions(config ReplayGainConfig) {
	p.player.SetReplayGainOptions(player.ReplayGainOptions{
		Mode:            player.ReplayGainMode(config.Mode),
		PreventClipping: config.PreventClipping,
		PreampGain:      config.PreampGainDB,
	})
}

// call BEFORE updating p.nowPlayingIdx
func (p *PlaybackManager) checkScrobble(playDur time.Duration) {
	if !p.scrobbleCfg.Enabled || len(p.playQueue) == 0 || p.nowPlayingIdx < 0 {
		return
	}
	if playDur.Seconds() < 0.1 || p.curTrackTime < 0.1 {
		return
	}
	pcnt := playDur.Seconds() / p.curTrackTime * 100
	timeThresholdMet := p.scrobbleCfg.ThresholdTimeSeconds >= 0 &&
		playDur.Seconds() >= float64(p.scrobbleCfg.ThresholdTimeSeconds)
	if timeThresholdMet || pcnt >= float64(p.scrobbleCfg.ThresholdPercent) {
		song := p.playQueue[p.nowPlayingIdx]
		log.Printf("Scrobbling %q", song.Title)
		song.PlayCount += 1
		p.lastScrobbled = song
		go p.sm.Server.Scrobble(song.ID, map[string]string{"time": strconv.FormatInt(time.Now().Unix()*1000, 10)})
	}
}

func (p *PlaybackManager) sendNowPlayingScrobble() {
	if !p.scrobbleCfg.Enabled || len(p.playQueue) == 0 || p.nowPlayingIdx < 0 {
		return
	}
	song := p.playQueue[p.nowPlayingIdx]
	go p.sm.Server.Scrobble(song.ID, map[string]string{
		"time":       strconv.FormatInt(time.Now().Unix()*1000, 10),
		"submission": "false",
	})
}

func (p *PlaybackManager) invokeOnSongChangeCallbacks() {
	if p.callbacksDisabled {
		return
	}
	for _, cb := range p.onSongChange {
		cb(p.NowPlaying(), p.lastScrobbled)
	}
	p.lastScrobbled = nil
}

func (p *PlaybackManager) startPollTimePos() {
	ctx, cancel := context.WithCancel(p.ctx)
	p.cancelPollPos = cancel
	p.pollingTick = time.NewTicker(250 * time.Millisecond)

	// TODO: fix occasional nil pointer dereference on app quit
	go func() {
		for {
			select {
			case <-ctx.Done():
				p.pollingTick.Stop()
				p.pollingTick = nil
				return
			case <-p.pollingTick.C:
				p.doUpdateTimePos()
			}
		}
	}()
}

func (p *PlaybackManager) doUpdateTimePos() {
	if p.callbacksDisabled {
		return
	}
	s := p.player.GetStatus()
	for _, cb := range p.onPlayTimeUpdate {
		cb(s.TimePos, s.Duration)
	}
}

func (p *PlaybackManager) stopPollTimePos() {
	if p.cancelPollPos != nil {
		p.cancelPollPos()
		p.cancelPollPos = nil
	}
	if p.pollingTick != nil {
		p.pollingTick.Stop()
	}
}
