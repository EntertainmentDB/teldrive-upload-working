package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"
	"uploader/config"
	"uploader/pkg/logger"
	"uploader/pkg/pb"
	"uploader/pkg/services"

	"flag"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/lib/pacer"
	"github.com/rclone/rclone/lib/rest"
	"go.uber.org/zap"
)

func main() {
	sourcePath := flag.String("path", "", "File or directory path to upload")
	destDir := flag.String("dest", "", "Remote directory for uploaded files")
	workers := flag.Int("workers", 0, "Number of current workers to use when uploading multi-parts")
	transfers := flag.Int("transfers", 0, "Number of current files to upload at once")
	flag.Parse()

	if *sourcePath == "" || *destDir == "" {
		if runtime.GOOS == "windows" {
			fmt.Println("Usage: ./uploader.exe -path <file_or_directory_path> -dest <remote_directory>")
			return
		}
		fmt.Println("Usage: ./uploader -path <file_or_directory_path> -dest <remote_directory>")
		return
	}

	config.InitConfig()
	config := config.GetConfig()

	numTransfers := config.Transfers
	if *transfers != 0 {
		numTransfers = *transfers
	}

	numWorkers := config.Workers
	if *workers != 0 {
		numWorkers = *workers
	}

	var wg sync.WaitGroup
	progress := pb.NewProgress(
		&wg,
		pb.OptionSetWriter(os.Stderr),
		pb.OptionSetThrottle(65*time.Millisecond),
	)

	fs.GetConfig(context.TODO()).LogLevel = fs.LogLevelDebug
	var log *zap.Logger
	if config.Debug {
		log = logger.InitLogger(logger.AddCustomWriter(progress.LogWriter))
	} else {
		log = logger.InitLogger()
	}
	fs.LogPrint = func(level fs.LogLevel, text string) {
		log.Debug(text)
	}

	authCookie := &http.Cookie{
		Name:  "user-session",
		Value: config.SessionToken,
	}

	ctx := context.Background()

	httpClient := rest.NewClient(http.DefaultClient).SetRoot(config.ApiURL).SetCookie(authCookie)

	pacer := fs.NewPacer(ctx, pacer.NewDefault(pacer.MinSleep(400*time.Millisecond),
		pacer.MaxSleep(5*time.Second), pacer.DecayConstant(2), pacer.AttackConstant(0)))

	// progress := mpb.New(mpb.WithWaitGroup(&wg))

	uploader := services.NewUploadService(
		httpClient,
		numWorkers,
		numTransfers,
		int64(config.PartSize),
		config.EncryptFiles,
		config.RandomisePart,
		config.ChannelID,
		config.DeleteAfterUpload,
		pacer,
		ctx,
		progress,
		&wg,
		log,
	)

	path := *destDir
	if len(path) == 0 || path[0] != '/' {
		path = "/" + path
	}

	err := uploader.CreateRemoteDir(path)

	if err != nil {
		log.Fatal("create remote dir failed", zap.Error(err))
	}

	stopProgress := uploader.Progress.StartProgress()

	if fileInfo, err := os.Stat(*sourcePath); err == nil {
		if fileInfo.IsDir() {
			info, err := uploader.GetFilesInDirectoryInfo(*sourcePath)
			if err != nil {
				log.Fatal("get files in directory info failed", zap.Error(err))
			}
			uploader.Progress.AddTransfer(info.TotalFiles, info.TotalSize)
			err = uploader.UploadFilesInDirectory(*sourcePath, path)
			if err != nil {
				log.Fatal("upload files in directory failed", zap.Error(err))
			}
		} else {
			uploader.Progress.AddTransfer(1, fileInfo.Size())
			err := uploader.UploadFile(*sourcePath, path)
			if err != nil {
				log.Fatal("upload failed", zap.Error(err))
			}
		}
	} else {
		log.Fatal("get sourcePath info failed", zap.Error(err))
	}
	uploader.Progress.Wait()
	stopProgress()

	log.Info("uploads complete!")
}
