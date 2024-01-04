package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"
	"uploader/pkg/logger"
	"uploader/pkg/pb"
	"uploader/pkg/services"
	"uploader/pkg/types"

	"flag"

	"github.com/kelseyhightower/envconfig"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/lib/pacer"
	"github.com/rclone/rclone/lib/rest"
	"go.uber.org/zap"

	"github.com/joho/godotenv"
)

func loadConfigFromEnv() (*types.Config, error) {

	var config types.Config

	err := godotenv.Load("upload.env")
	if err != nil {
		return nil, err
	}

	err = envconfig.Process("", &config)
	if err != nil {
		panic(err)
	}
	if config.PartSize == 0 {
		config.PartSize = 1000 * fs.Mebi
	}

	return &config, nil
}

func main() {
	sourcePath := flag.String("path", "", "File or directory path to upload")
	destDir := flag.String("dest", "", "Remote directory for uploaded files")
	workers := flag.String("workers", "", "Number of current workers to use when uploading multi-parts")
	transfers := flag.String("transfers", "", "Number of current files to upload at once")
	flag.Parse()

	if *sourcePath == "" || *destDir == "" {
		if runtime.GOOS == "windows" {
			fmt.Println("Usage: ./uploader.exe -path <file_or_directory_path> -dest <remote_directory>")
			return
		}
		fmt.Println("Usage: ./uploader -path <file_or_directory_path> -dest <remote_directory>")
		return
	}

	config, err := loadConfigFromEnv()

	numTransfers := config.Transfers
	if *transfers != "" {
		numTransfers, err = strconv.Atoi(*transfers)
	}
	if err != nil {
		log.Fatal("transfers flag must be a number", zap.Error(err))
	}

	numWorkers := config.Workers
	if *workers != "" {
		numWorkers, err = strconv.Atoi(*workers)
	}
	if err != nil {
		log.Fatal("workers flag must be a number", zap.Error(err))
	}

	if err != nil {
		log.Fatalln(err)
	}

	var wg sync.WaitGroup
	// prg := progress.NewProgress(&wg)
	// progressWriterAdapter := &logger.ProgressWriterAdapter{Progress: prg}
	log := logger.InitLogger()

	authCookie := &http.Cookie{
		Name:  "user-session",
		Value: config.SessionToken,
	}

	ctx := context.Background()

	httpClient := rest.NewClient(http.DefaultClient).SetRoot(config.ApiURL).SetCookie(authCookie)

	pacer := fs.NewPacer(ctx, pacer.NewDefault(pacer.MinSleep(400*time.Millisecond),
		pacer.MaxSleep(5*time.Second), pacer.DecayConstant(2), pacer.AttackConstant(0)))

	// progress := mpb.New(mpb.WithWaitGroup(&wg))
	progress := pb.NewProgress(
		&wg,
		pb.OptionSetWriter(os.Stderr),
		pb.OptionThrottle(65*time.Millisecond),
	)

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

	err = uploader.CreateRemoteDir(path)

	if err != nil {
		log.Fatal("create remote failed", zap.Error(err))
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
				log.Fatal("upload failed", zap.Error(err))
			}
		} else {
			if err := uploader.UploadFile(*sourcePath, path); err != nil {
				log.Fatal("upload failed", zap.Error(err))
			}
		}
	} else {
		log.Fatal(err.Error())
	}
	uploader.Progress.Wait()
	stopProgress()

	log.Info("Uploads complete!")
}
