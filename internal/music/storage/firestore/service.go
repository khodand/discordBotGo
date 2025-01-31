package firestore

import (
	"math/rand"
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/HalvaPovidlo/discordBotGo/internal/pkg"
	"github.com/HalvaPovidlo/discordBotGo/pkg/contexts"
)

type shortCache struct {
	sync.RWMutex
	List []pkg.SongID
}

type Service struct {
	songs  *SongsCache
	client *Client

	songsShort   shortCache
	updatesMutex sync.Mutex
	updated      bool
}

func NewFirestoreService(ctx contexts.Context, client *Client, songs *SongsCache) (*Service, error) {
	f := Service{
		songs:      songs,
		client:     client,
		songsShort: shortCache{},
	}
	go f.updateShortCache(ctx)
	f.updateShortCacheProcess(ctx)
	return &f, nil
}

func (s *Service) GetSong(ctx contexts.Context, id pkg.SongID) (*pkg.Song, error) {
	key := s.songs.KeyFromID(id)
	log := ctx.LoggerFromContext()
	log.Debugf("Get song %s from cache", id)
	if s, ok := s.songs.Get(key); ok {
		return s, nil
	}

	log.Debugf("Get song %s from db", id)
	song, err := s.client.GetSongByID(ctx, id)
	if err != nil {
		if err == ErrNotFound {
			return nil, ErrNotFound
		}
		return nil, errors.Wrapf(err, "get song by id %s", id)
	}
	log.Debugf("Set song %s to cache", id)
	s.songs.Set(key, song)
	return song, nil
}

func (s *Service) SetSong(ctx contexts.Context, song *pkg.Song) error {
	s.setUpdate(true)
	if err := s.client.SetSong(ctx, song); err != nil {
		return errors.Wrap(err, "firestore set song")
	}
	s.songs.Set(s.songs.KeyFromID(song.ID), song)
	return nil
}

func (s *Service) UpsertSongIncPlaybacks(ctx contexts.Context, new *pkg.Song) (int, error) {
	log := ctx.LoggerFromContext()
	log.Debug("UpsertSongIncPlaybacks new", new)
	old, err := s.GetSong(ctx, new.ID)
	log.Debug("UpsertSongIncPlaybacks old", old)
	if err != nil && err != ErrNotFound {
		return 0, errors.Wrap(err, "failed to get song from db")
	}
	playbacks := 0
	new.MergeNoOverride(old)
	new.Playbacks++
	playbacks = new.Playbacks
	if err = s.SetSong(ctx, new); err != nil {
		return 0, errors.Wrap(err, "failed to set song into db")
	}
	return playbacks, nil
}

func (s *Service) IncrementUserRequests(ctx contexts.Context, song *pkg.Song, userID string) {
	userSong, err := s.client.GetUserSong(ctx, song.ID, userID)
	if err != nil {
		if err == ErrNotFound {
			song.Playbacks = 1
		} else {
			return
		}
	} else {
		song.Playbacks = userSong.Playbacks + 1
	}
	err = s.client.SetUserSong(ctx, song, userID)
	if err != nil {
		return
	}
}

func (s *Service) GetRandomSongs(ctx contexts.Context, n int) ([]*pkg.Song, error) {
	set := make(map[string]pkg.SongID)
	max := len(s.songsShort.List)
	if max == 0 {
		return nil, errors.New("no preloaded songs")
	}

	cooldown := n * 10
	for len(set) < n && cooldown > 0 {
		cooldown--
		rand.Seed(time.Now().UnixNano())
		time.Sleep(time.Nanosecond * 2)
		i := rand.Intn(max)
		s.songsShort.RLock()
		set[s.songsShort.List[i].ID] = s.songsShort.List[i]
		s.songsShort.RUnlock()
	}

	result := make([]*pkg.Song, 0, len(set))
	for _, v := range set {
		song, err := s.GetSong(ctx, v)
		if err != nil {
			return nil, errors.Wrap(err, "get song failed")
		}
		result = append(result, song)
	}
	return result, nil
}

func (s *Service) updateShortCacheProcess(ctx contexts.Context) {
	// TODO: in config
	ticker := time.NewTicker(3 * time.Hour)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if s.needUpdate() {
					s.setUpdate(false)
					s.updateShortCache(ctx)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (s *Service) updateShortCache(ctx contexts.Context) {
	list, err := s.client.GetAllSongsID(ctx)
	if err != nil {
		s.setUpdate(true)
		ctx.LoggerFromContext().Error(errors.Wrap(err, "getting all songs"))
	}
	s.songsShort.Lock()
	s.songsShort.List = list
	size := len(list)
	s.songsShort.Unlock()
	ctx.LoggerFromContext().Infof("short cache updated with %d songs", size)
}

func (s *Service) setUpdate(b bool) {
	s.updatesMutex.Lock()
	s.updated = b
	s.updatesMutex.Unlock()
}

func (s *Service) needUpdate() bool {
	s.updatesMutex.Lock()
	defer s.updatesMutex.Unlock()
	return s.updated
}
