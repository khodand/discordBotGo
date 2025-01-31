package main

import (
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	ytdl "github.com/kkdai/youtube/v2"
	"github.com/pkg/errors"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"

	"github.com/HalvaPovidlo/discordBotGo/cmd/config"
	"github.com/HalvaPovidlo/discordBotGo/docs"
	v1 "github.com/HalvaPovidlo/discordBotGo/internal/api/v1"
	capi "github.com/HalvaPovidlo/discordBotGo/internal/chess/api/discord"
	"github.com/HalvaPovidlo/discordBotGo/internal/chess/lichess"
	dapi "github.com/HalvaPovidlo/discordBotGo/internal/music/api/discord"
	musicrest "github.com/HalvaPovidlo/discordBotGo/internal/music/api/rest"
	"github.com/HalvaPovidlo/discordBotGo/internal/music/audio"
	"github.com/HalvaPovidlo/discordBotGo/internal/music/player"
	ytsearch "github.com/HalvaPovidlo/discordBotGo/internal/music/search/youtube"
	"github.com/HalvaPovidlo/discordBotGo/internal/music/storage/firestore"
	"github.com/HalvaPovidlo/discordBotGo/pkg/contexts"
	dpkg "github.com/HalvaPovidlo/discordBotGo/pkg/discord"
	"github.com/HalvaPovidlo/discordBotGo/pkg/zap"
)

// @title           HalvaBot for Discord
// @version         1.0
// @description     A music discord bot.

// @license.name  Apache 2.0
// @license.url   http://www.apache.org/licenses/LICENSE-2.0.html

// @host      localhost:9091
// @BasePath  /api/v1
func main() {
	// TODO: all magic vars to config
	cfg, err := config.InitConfig()
	if err != nil {
		panic(errors.Wrap(err, "config read failed"))
	}
	logger := zap.NewLogger(cfg.General.Debug)
	ctx, cancel := contexts.WithLogger(contexts.Background(), logger)

	// Initialize discord session
	session, err := dpkg.OpenSession(cfg.Discord.Token, cfg.General.Debug, logger)
	if err != nil {
		panic(errors.Wrap(err, "discord open session failed"))
	}
	defer func() {
		err = session.Close()
		if err != nil {
			logger.Error(errors.Wrap(err, "close session"))
		} else {
			logger.Infow("Bot session closed")
		}
	}()

	// Cache
	songsCache := firestore.NewSongsCache(ctx, 24*time.Hour)
	defer songsCache.Clear()

	// YouTube services
	ytService, err := youtube.NewService(ctx, option.WithCredentialsFile("halvabot-google.json"))
	if err != nil {
		panic(errors.Wrap(err, "youtube init failed"))
	}
	ytClient := ytsearch.NewYouTubeClient(
		&ytdl.Client{
			Debug:      cfg.General.Debug,
			HTTPClient: http.DefaultClient,
		},
		ytService,
		songsCache,
		cfg.Youtube,
	)

	// Firestore stage
	fireStorage, err := firestore.NewFirestoreClient(ctx, "halvabot-firebase.json", cfg.General.Debug)
	if err != nil {
		panic(err)
	}
	fireService, err := firestore.NewFirestoreService(ctx, fireStorage, songsCache)
	if err != nil {
		panic(err)
	}

	// Music stage
	voiceClient := audio.NewVoiceClient(session)
	rawAudioPlayer := audio.NewPlayer(&cfg.Discord.Voice.EncodeOptions, logger)
	musicPlayer := player.NewMusicService(ctx, fireService, ytClient, voiceClient, rawAudioPlayer, logger)

	// Chess
	lichessClient := lichess.NewClient()

	// Discord commands
	musicCog := dapi.NewCog(ctx, musicPlayer, cfg.Discord.Prefix, logger, cfg.Discord.API)
	musicCog.RegisterCommands(session, cfg.General.Debug, logger)
	chessCog := capi.NewCog(ctx, cfg.Discord.Prefix, lichessClient, logger)
	chessCog.RegisterCommands(session, cfg.General.Debug, logger)

	// Http routers
	if !cfg.General.Debug {
		gin.SetMode(gin.ReleaseMode)
		gin.DisableConsoleColor()
	}
	router := gin.New()
	docs.SwaggerInfo.Host = cfg.Host.IP + ":" + cfg.Host.Bot
	docs.SwaggerInfo.BasePath = "/api/v1"
	apiRouter := v1.NewAPI(router.Group("/api/v1")).Router()
	musicrest.NewHandler(musicPlayer, apiRouter).Router()
	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	go func() {
		err := router.Run(":" + cfg.Host.Bot)
		if err != nil {
			logger.Error(err)
			return
		}
	}()

	// TODO: Graceful shutdown
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc
	cancel()

	logger.Infow("Graceful shutdown")
	_ = logger.Sync()
}
