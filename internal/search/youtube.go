package search

import (
	"sort"

	ytdl "github.com/kkdai/youtube/v2"
	"github.com/kkdai/youtube/v2/downloader"
	"github.com/pkg/errors"
	"google.golang.org/api/youtube/v3"

	"github.com/HalvaPovidlo/discordBotGo/internal/pkg"
	"github.com/HalvaPovidlo/discordBotGo/pkg/contexts"
)

const (
	videoPrefix     = "https://youtube.com/watch?v="
	channelPrefix   = "https://youtube.com/channel/"
	videoKind       = "youtube#video"
	maxSearchResult = 10
)

var (
	ErrSongNotFound = errors.New("song not found")
)

type YouTubeConfig struct {
	Download  bool   `json:"download"`
	OutputDir string `json:"output"`
}

type YouTube struct {
	ytdl    *ytdl.Client
	youtube *youtube.Service
	config  YouTubeConfig
}

func NewYouTubeClient(ytdl *ytdl.Client, yt *youtube.Service, config YouTubeConfig) *YouTube {
	return &YouTube{
		ytdl:    ytdl,
		youtube: yt,
		config:  config,
	}
}

func getImages(details *youtube.ThumbnailDetails) (artwork, thumbnail string) {
	artwork = ""
	thumbnail = ""
	if details != nil {
		if details.Default != nil {
			thumbnail = details.Default.Url
			artwork = details.Default.Url
		}
		if details.Standard != nil {
			thumbnail = details.Standard.Url
			artwork = details.Standard.Url
		}
		if details.Medium != nil {
			artwork = details.Medium.Url
		}
		if details.High != nil {
			artwork = details.High.Url
		}
		if details.Maxres != nil {
			artwork = details.Maxres.Url
		}
	}
	return
}

func (y *YouTube) findSong(ctx contexts.Context, query string) (*pkg.Song, error) {
	call := y.youtube.Search.List([]string{"id, snippet"}).
		Q(query).
		MaxResults(maxSearchResult)
	call.Context(ctx)
	response, err := call.Do()
	if err != nil || response.Items == nil {
		// TODO: NOT FOUND?
		return nil, errors.Wrap(err, "could not find any results for the specified query")
	}

	for _, item := range response.Items {
		if item.Id.Kind == videoKind {
			art, thumb := getImages(item.Snippet.Thumbnails)
			return &pkg.Song{
				Title:        item.Snippet.Title,
				URL:          videoPrefix + item.Id.VideoId,
				Service:      pkg.ServiceYouTube,
				ArtistName:   item.Snippet.ChannelTitle,
				ArtistURL:    channelPrefix + item.Snippet.ChannelId,
				ArtworkURL:   art,
				ThumbnailURL: thumb,
				ID: pkg.SongID{
					ID:      item.Id.VideoId,
					Service: pkg.ServiceYouTube,
				},
			}, nil
		}
	}
	return nil, ErrSongNotFound
}

func (y *YouTube) ensureStreamInfo(ctx contexts.Context, song *pkg.Song) (*pkg.Song, error) {
	dl := downloader.Downloader{
		Client:    *y.ytdl,
		OutputDir: y.config.OutputDir,
	}
	url := song.URL
	videoInfo, err := y.ytdl.GetVideo(url)
	if err != nil {
		return nil, errors.Wrapf(err, "loag video metadata by url %s", url)
	}
	formats := videoInfo.Formats
	if len(formats) == 0 {
		return nil, errors.New("unable to get list of formats")
	}
	sort.SliceStable(formats, func(i, j int) bool {
		return formats[i].ItagNo < formats[j].ItagNo
	})
	format := formats[0]

	if y.config.Download {
		// TODO: NOT IMPLEMENTED!!!!
		err := dl.Download(ctx, videoInfo, &format, videoInfo.ID)
		if err != nil {
			return nil, err
		}
	} else {
		streamURL, err := y.ytdl.GetStreamURLContext(ctx, videoInfo, &format)
		if err != nil {
			return nil, errors.Wrapf(err, "unable to get streamURL %s", videoInfo.Title)
		}
		song.StreamURL = streamURL
		song.Duration = videoInfo.Duration.Seconds()
		return song, nil
	}

	return nil, nil
}

func (y *YouTube) FindSong(ctx contexts.Context, query string) (*pkg.Song, error) {
	logger := ctx.LoggerFromContext()
	logger.Debug("finding song")
	song, err := y.findSong(ctx, query)
	if err != nil {
		if err == ErrSongNotFound {
			return nil, ErrSongNotFound
		}
		return nil, errors.Wrap(err, "search in youtube")
	}

	song, err = y.ensureStreamInfo(ctx, song)
	if err != nil {
		return nil, errors.Wrap(err, "ensure stream info")
	}
	return song, nil
}
