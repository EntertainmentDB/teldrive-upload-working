package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
	"uploader/pkg/logger"
	"uploader/pkg/services"
	"uploader/pkg/types"

	"flag"

	"github.com/kelseyhightower/envconfig"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/lib/pacer"
	"github.com/rclone/rclone/lib/rest"
	"github.com/vbauerster/mpb/v8"

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
		fmt.Println("Usage: ./uploader -path <file_or_directory_path> -dest <remote_directory>")
		return
	}

	config, err := loadConfigFromEnv()

	if err != nil {
		logger.Error.Fatalln(err)
	}

	authCookie := &http.Cookie{
		Name:  "user-session",
		Value: config.SessionToken,
	}

	ctx := context.Background()

	httpClient := rest.NewClient(http.DefaultClient).SetRoot(config.ApiURL).SetCookie(authCookie)

	pacer := fs.NewPacer(ctx, pacer.NewDefault(pacer.MinSleep(400*time.Millisecond),
		pacer.MaxSleep(5*time.Second), pacer.DecayConstant(2), pacer.AttackConstant(0)))

	var wg sync.WaitGroup
	progress := mpb.New(mpb.WithWaitGroup(&wg))

	numTransfers := config.Transfers
	if *transfers != "" {
		numTransfers, err = strconv.Atoi(*transfers)
	}
	if err != nil {
		logger.Error.Fatalln("transfers flag must be a number", err)
	}

	numWorkers := config.Workers
	if *workers != "" {
		numWorkers, err = strconv.Atoi(*workers)
	}
	if err != nil {
		logger.Error.Fatalln("workers flag must be a number", err)
	}

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
		&wg)

	err = uploader.CreateRemoteDir(*destDir)

	if err != nil {
		logger.Error.Fatalln(err)
	}

	if fileInfo, err := os.Stat(*sourcePath); err == nil {
		if fileInfo.IsDir() {
			err := uploader.UploadFilesInDirectory(*sourcePath, *destDir)
			if err != nil {
				logger.Error.Println("upload failed:", err)
			}
			uploader.Progress.Wait()
		} else {
			if err := uploader.UploadFile(*sourcePath, *destDir); err != nil {
				logger.Error.Println("upload failed:", err)
			}
		}
	} else {
		logger.Error.Fatalln(err)
	}

	logger.Info.Println("Uploads complete!")
}
