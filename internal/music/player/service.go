package player

import (
	"io"
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/HalvaPovidlo/discordBotGo/internal/music/audio"
	"github.com/HalvaPovidlo/discordBotGo/internal/pkg"
	"github.com/HalvaPovidlo/discordBotGo/pkg/contexts"
	"github.com/HalvaPovidlo/discordBotGo/pkg/zap"
)

type Firestore interface {
	UpsertSongIncPlaybacks(ctx contexts.Context, new *pkg.Song) (int, error)
	IncrementUserRequests(ctx contexts.Context, song *pkg.Song, userID string)
	GetRandomSongs(ctx contexts.Context, n int) ([]*pkg.Song, error)
}

type YouTube interface {
	FindSong(ctx contexts.Context, query string) (*pkg.Song, error)
	EnsureStreamInfo(ctx contexts.Context, song *pkg.Song) (*pkg.Song, error)
}

type Service struct {
	*Player
	storage Firestore
	youtube YouTube

	radioMutex sync.Mutex
	isRadio    bool
	logger     zap.Logger
}

func NewMusicService(ctx contexts.Context, storage Firestore, youtube YouTube, voice VoiceClient, audio MediaPlayer, logger zap.Logger) *Service {
	s := &Service{
		Player:  NewPlayer(ctx, voice, audio, logger),
		storage: storage,
		youtube: youtube,
		logger:  logger,
	}
	s.Player.SubscribeOnErrors(s.handleError)
	return s
}

func (s *Service) Play(ctx contexts.Context, query, userID, guildID, channelID string) (*pkg.Song, int, error) {
	if !s.Player.voice.IsConnected() && (channelID == "" || guildID == "") {
		return nil, 0, ErrNotConnected
	}

	s.logger.Debug("Finding song")
	song, err := s.youtube.FindSong(ctx, query)
	if err != nil {
		return nil, 0, errors.Wrap(err, "find and load song from youtube")
	}

	if channelID != "" || guildID != "" {
		s.Connect(guildID, channelID)
	}

	song.LastPlay = pkg.PlayDate{Time: time.Now()}
	playbacks, err := s.storage.UpsertSongIncPlaybacks(ctx, song)
	if err != nil {
		err = errors.Wrap(err, "upsert song with increment")
	}

	if userID != "" {
		s.storage.IncrementUserRequests(ctx, song, userID)
	}

	go s.Player.Play(song)
	return song, playbacks, err
}

func (s *Service) Random(ctx contexts.Context, n int) ([]*pkg.Song, error) {
	return s.storage.GetRandomSongs(ctx, n)
}

func (s *Service) SetRadio(ctx contexts.Context, b bool, guildID, channelID string) error {
	s.setRadio(b)
	if !b {
		return nil
	}
	if !s.Player.voice.IsConnected() {
		if guildID == "" || channelID == "" {
			return ErrNotConnected
		}
		s.Player.Connect(guildID, channelID)
	}
	if s.NowPlaying() == nil {
		return s.playRandomSong(ctx)
	}
	return nil
}

func (s *Service) setRadio(b bool) {
	s.radioMutex.Lock()
	s.isRadio = b
	s.radioMutex.Unlock()
}

func (s *Service) playRandomSong(ctx contexts.Context) error {
	songs, err := s.storage.GetRandomSongs(ctx, 1)
	if err != nil {
		return errors.Wrap(err, "get 1 random song from bd")
	}
	song := songs[0]
	if song.StreamURL == "" {
		song, err = s.youtube.EnsureStreamInfo(ctx, song)
		if err != nil {
			s.logger.Error(errors.Wrap(err, "ensure stream info for radio"))
			return s.playRandomSong(ctx)
		}
	}
	s.Player.Play(song)
	return nil
}

func (s *Service) RadioStatus() bool {
	s.radioMutex.Lock()
	b := s.isRadio
	s.radioMutex.Unlock()
	return b
}

func (s *Service) handleError(err error) {
	if errors.Is(err, ErrQueueEmpty) {
		if s.RadioStatus() {
			err := s.playRandomSong(contexts.Context{Context: contexts.Background()})
			if err != nil {
				s.logger.Error(errors.Wrap(err, "radio failed"))
				s.setRadio(false)
			}
		}
		return
	}
	if !errors.Is(err, audio.ErrManualStop) && !errors.Is(err, io.EOF) {
		s.setRadio(false)
		s.logger.Error("error from player", err)
	}
}

func (s *Service) SubscribeOnErrors(h ErrorHandler) {
	s.Player.SubscribeOnErrors(func(err error) {
		if errors.Is(err, io.EOF) || errors.Is(err, audio.ErrManualStop) || errors.Is(err, ErrQueueEmpty) {
			return
		}
		h(err)
	})
}

func (s *Service) Stop() {
	s.setRadio(false)
	s.Player.Stop()
}

func (s *Service) Disconnect() {
	s.setRadio(false)
	s.Player.Disconnect()
}

func (s *Service) Status() pkg.PlayerStatus {
	return pkg.PlayerStatus{
		Loop:  s.LoopStatus(),
		Radio: s.RadioStatus(),
		Song:  s.SongStatus(),
		Now:   s.NowPlaying(),
	}
}
