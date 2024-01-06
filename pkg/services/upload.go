package services

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"uploader/pkg/pb"
	"uploader/pkg/types"

	"github.com/gofrs/uuid"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/fserrors"
	"github.com/rclone/rclone/lib/rest"
	"go.uber.org/zap"
)

var retryErrorCodes = []int{
	429, // Too Many Requests.
	500, // Internal Server Error
	502, // Bad Gateway
	503, // Service Unavailable
	504, // Gateway Timeout
	509, // Bandwidth Limit Exceeded
}

type UploadService struct {
	http              *rest.Client
	numWorkers        int
	concurrentFiles   chan struct{}
	partSize          int64
	encryptFiles      bool
	randomisePart     bool
	channelID         int64
	deleteAfterUpload bool
	pacer             *fs.Pacer
	ctx               context.Context
	Progress          *pb.Progress
	wg                *sync.WaitGroup
	logger            *zap.Logger
}

func NewUploadService(http *rest.Client, numWorkers int, numTransfers int, partSize int64, encryptFiles bool, randomisePart bool, channelID int64, deleteAfterUpload bool, pacer *fs.Pacer, ctx context.Context, progress *pb.Progress, wg *sync.WaitGroup, logger *zap.Logger) *UploadService {
	return &UploadService{
		http:              http,
		numWorkers:        numWorkers,
		concurrentFiles:   make(chan struct{}, numTransfers),
		partSize:          partSize,
		encryptFiles:      encryptFiles,
		randomisePart:     randomisePart,
		channelID:         channelID,
		deleteAfterUpload: deleteAfterUpload,
		pacer:             pacer,
		ctx:               ctx,
		wg:                wg,
		Progress:          progress,
		logger:            logger,
	}
}

func shouldRetry(ctx context.Context, resp *http.Response, err error) (bool, error) {
	if fserrors.ContextError(ctx, &err) {
		return false, err
	}
	return fserrors.ShouldRetry(err) || fserrors.ShouldRetryHTTP(resp, retryErrorCodes), err
}

func (u *UploadService) checkFileExists(fileName string, path string) (bool, error) {
	opts := rest.Opts{
		Method: "GET",
		Path:   "/api/files",
		Parameters: url.Values{
			"path": []string{path},
			"op":   []string{"find"},
			"name": []string{fileName},
		},
	}

	var err error
	var info types.ReadMetadataResponse
	var resp *http.Response

	err = u.pacer.Call(func() (bool, error) {
		resp, err = u.http.CallJSON(u.ctx, &opts, nil, &info)
		return shouldRetry(u.ctx, resp, err)
	})
	if err != nil {
		return false, err
	}
	if resp != nil && resp.StatusCode != 404 && len(info.Files) > 0 {
		return true, nil
	}

	return false, nil
}

func (u *UploadService) UploadFile(filePath string, destDir string) error {
	file, err := os.Open(filePath)
	if err != nil {
		u.logger.Fatal("open file failed", zap.String("filePath", filePath), zap.Error(err))
		return err
	}
	defer file.Close()

	buffer := make([]byte, 512)
	_, err = file.Read(buffer)
	if err != nil {
		u.logger.Fatal("read file failed", zap.String("filePath", filePath), zap.Error(err))
		return err
	}

	mimeType := http.DetectContentType(buffer)

	fileInfo, _ := file.Stat()
	fileSize := fileInfo.Size()
	fileName := filepath.Base(filePath)

	bar := pb.NewOptions64(fileSize,
		pb.OptionShowCount(),
		pb.OptionEnableColorCodes(true),
		pb.OptionShowBytes(true),
		pb.OptionSetWidth(10),
		pb.OptionSetDescription(fileName),
		pb.OptionSetTheme(pb.Theme{
			Saucer:        "[green]=[reset]",
			SaucerHead:    "[green]>[reset]",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
		pb.OptionFullWidth(),
		pb.OptionSetRenderBlankState(true))

	defer bar.Close()

	u.Progress.AddBar(bar)

	exists, err := u.checkFileExists(fileName, destDir)
	if err != nil {
		bar.Abort()
		u.logger.Error("check file exists failed", zap.String("fileName", fileName), zap.String("destDir", destDir), zap.Error(err))
		return err
	}
	if exists {
		u.Progress.AddExisting(float64(fileSize))
		u.logger.Info("file exists", zap.String("fileName", fileName))
		return nil
	}

	input := fmt.Sprintf("%s:%s:%d", fileName, destDir, fileSize)

	hash := md5.Sum([]byte(input))
	hashString := hex.EncodeToString(hash[:])

	uploadURL := fmt.Sprintf("/api/uploads/%s", hashString)

	var existingParts map[int]types.PartFile
	var uploadFile types.UploadFile

	if u.partSize < fileSize {
		opts := rest.Opts{
			Method: "GET",
			Path:   uploadURL,
		}

		err := u.pacer.Call(func() (bool, error) {
			resp, err := u.http.CallJSON(u.ctx, &opts, nil, &uploadFile)
			return shouldRetry(u.ctx, resp, err)
		})
		if err == nil {
			existingParts = make(map[int]types.PartFile, len(uploadFile.Parts))
			for _, part := range uploadFile.Parts {
				existingParts[part.PartNo] = part
			}
		}
	}

	var wg sync.WaitGroup

	totalParts := fileSize / u.partSize
	if fileSize%u.partSize != 0 {
		totalParts++
	}

	uploadedParts := make(chan types.PartFile, totalParts)
	concurrentWorkers := make(chan struct{}, u.numWorkers)

	channelID := u.channelID

	encryptFile := u.encryptFiles

	if len(uploadFile.Parts) > 0 {
		channelID = uploadFile.Parts[0].ChannelID

		encryptFile = uploadFile.Parts[0].Encrypted
	}

	// var bars *mpb.Bar
	// barOptions := []mpb.BarOption{
	// 	mpb.PrependDecorators(
	// 		decor.Name("shortenedName", decor.WC{C: decor.DSyncWidthR | decor.DextraSpace}),
	// 		decor.Name(" ("),
	// 		decor.Percentage(decor.WCSyncSpace, decor.WC{C: decor.DSyncWidthR}),
	// 		decor.Name(")  "),
	// 		decor.Counters(decor.SizeB1000(0), "% .2f/% .2f", decor.WC{C: decor.DSyncWidthR}),
	// 	), mpb.AppendDecorators(
	// 		// decor.EwmaETA(decor.ET_STYLE_GO, 60),
	// 		decor.AverageETA(decor.ET_STYLE_GO),
	// 		decor.Name(" | "),
	// 		// decor.OnComplete(decor.EwmaSpeed(decor.SizeB1000(0), "% .2f", 60, decor.WC{C: decor.DSyncWidthR}), "completed"),
	// 		decor.OnComplete(decor.AverageSpeed(decor.SizeB1000(0), "% .2f", decor.WC{C: decor.DSyncWidthR}), "completed"),
	// 	),
	// }

	// bar = u.pb.AddBar(fileSize,
	// 	barOptions...,
	// )
	// myBar := u.pb.AddBar(fileName, fileSize)
	// stopProgress := pb.StartProgress()
	// u.progress = mpb.New(mpb.WithWidth(64))
	// bar = u.pb.New(fileSize,
	// 	mpb.BarStyle().Rbound("|"),
	// 	barOptions...,
	// )

	go func() {
		wg.Wait()
		close(uploadedParts)
		bar.Finish()
	}()

	partName := fileName

	for i := int64(0); i < totalParts; i++ {
		start := i * u.partSize
		end := start + u.partSize
		if end > fileSize {
			end = fileSize
		}

		wg.Add(1)
		concurrentWorkers <- struct{}{}

		go func(partNumber int64, start, end int64) {
			defer wg.Done()
			defer func() {
				<-concurrentWorkers
			}()

			file, err := os.Open(filePath)
			if err != nil {
				u.logger.Error("open file failed", zap.String("filePath", filePath), zap.Error(err))
				return
			}
			defer file.Close()
			if existing, ok := existingParts[int(partNumber)+1]; ok {
				uploadedParts <- existing
				bar.IncrInt64(existing.Size)
				return
			}

			_, err = file.Seek(start, io.SeekStart)

			if err != nil {
				u.logger.Error("seek file failed", zap.String("filePath", filePath), zap.Error(err))
				return
			}

			pr := bar.ProxyReader(file)

			contentLength := end - start
			reader := io.LimitReader(pr, contentLength)

			if u.randomisePart {
				u1, _ := uuid.NewV4()
				partName = hex.EncodeToString(u1.Bytes())
			} else if totalParts > 1 {
				partName = fmt.Sprintf("%s.part.%03d", fileName, partNumber+1)
			}

			opts := rest.Opts{
				Method:        "POST",
				Path:          uploadURL,
				Body:          reader,
				ContentLength: &contentLength,
				Parameters: url.Values{
					"partName":  []string{partName},
					"fileName":  []string{fileName},
					"partNo":    []string{strconv.FormatInt(partNumber+1, 10)},
					"channelId": []string{strconv.FormatInt(int64(channelID), 10)},
					"encrypted": []string{strconv.FormatBool(encryptFile)},
				},
			}

			var partFile types.PartFile
			resp, err := u.http.CallJSON(context.TODO(), &opts, nil, &partFile)

			if err != nil {
				u.logger.Error("send part file failed", zap.String("filePath", filePath), zap.Error(err))
				return
			}
			if resp.StatusCode == 201 {
				uploadedParts <- partFile
				u.logger.Debug("part file sent", zap.String("fileName", fileName), zap.String("partName", partName), zap.Int64("partNumber", partNumber+1), zap.Int64("size", partFile.Size))
			}
		}(i, start, end)
	}

	var parts []types.FilePart
	for uploadPart := range uploadedParts {
		if uploadPart.PartId != 0 {
			parts = append(parts, types.FilePart{ID: int64(uploadPart.PartId), PartNo: uploadPart.PartNo, Salt: uploadPart.Salt})
		}
	}

	if len(parts) != int(totalParts) {
		bar.Abort()
		u.logger.Error("uploaded parts incomplete", zap.String("fileName", fileName), zap.Int("uploadedParts", len(parts)), zap.Int64("totalParts", totalParts))
		return fmt.Errorf("uploaded parts incomplete")
	}
	// bar.Wait()

	sort.Slice(parts, func(i, j int) bool {
		return parts[i].PartNo < parts[j].PartNo
	})

	filePayload := types.FilePayload{
		Name:      fileName,
		Type:      "file",
		Parts:     parts,
		MimeType:  mimeType,
		Path:      destDir,
		Size:      fileSize,
		ChannelID: channelID,
		Encrypted: encryptFile,
	}

	json.Marshal(filePayload)

	if err != nil {
		return err
	}

	opts := rest.Opts{
		Method: "POST",
		Path:   "/api/files",
	}

	err = u.pacer.Call(func() (bool, error) {
		resp, err := u.http.CallJSON(u.ctx, &opts, &filePayload, nil)
		return shouldRetry(u.ctx, resp, err)
	})

	if err != nil {
		return err
	}

	err = u.pacer.Call(func() (bool, error) {
		resp, err := u.http.CallJSON(u.ctx, &rest.Opts{Method: "DELETE", Path: uploadURL}, nil, nil)
		return shouldRetry(u.ctx, resp, err)
	})

	if err != nil {
		return err
	}

	u.logger.Info("file sent", zap.String("fileName", fileName), zap.Int64("fileSize", fileSize))

	return nil
}
func (u *UploadService) CreateRemoteDir(path string) error {
	opts := rest.Opts{
		Method: "POST",
		Path:   "/api/files/directories",
	}

	if len(path) == 0 || path[0] != '/' {
		path = "/" + path
	}

	mkdir := types.CreateDirRequest{
		Path: path,
	}

	err := u.pacer.Call(func() (bool, error) {
		resp, err := u.http.CallJSON(u.ctx, &opts, &mkdir, nil)
		return shouldRetry(u.ctx, resp, err)
	})

	if err != nil {
		return err
	}
	return nil
}

func (u *UploadService) readMetaDataForPath(path string, options *types.MetadataRequestOptions) (*types.ReadMetadataResponse, error) {

	opts := rest.Opts{
		Method: "GET",
		Path:   "/api/files",
		Parameters: url.Values{
			"path":          []string{path},
			"perPage":       []string{strconv.FormatUint(options.PerPage, 10)},
			"sort":          []string{"name"},
			"order":         []string{"asc"},
			"op":            []string{"list"},
			"nextPageToken": []string{options.NextPageToken},
		},
	}
	var err error
	var info types.ReadMetadataResponse
	var resp *http.Response

	err = u.pacer.Call(func() (bool, error) {
		resp, err = u.http.CallJSON(u.ctx, &opts, nil, &info)
		return shouldRetry(u.ctx, resp, err)
	})

	if err != nil && resp != nil && resp.StatusCode == 404 {
		return nil, fs.ErrorDirNotFound
	}

	if err != nil {
		return nil, err
	}

	return &info, nil
}

func (u *UploadService) list(path string) (files []types.FileInfo, err error) {

	var limit uint64 = 500
	var nextPageToken string = ""
	for {
		opts := &types.MetadataRequestOptions{
			PerPage:       limit,
			NextPageToken: nextPageToken,
		}

		info, err := u.readMetaDataForPath(path, opts)
		if err != nil {
			return nil, err
		}

		files = append(files, info.Files...)

		nextPageToken = info.NextPageToken
		if nextPageToken == "" {
			break
		}
	}
	return files, nil
}

func (u *UploadService) checkFileExistsInDirectory(name string, files []types.FileInfo) bool {
	for _, item := range files {
		if item.Name == name {
			return true
		}
	}
	return false
}

func (u *UploadService) UploadFilesInDirectory(sourcePath string, destDir string) error {
	entries, err := os.ReadDir(sourcePath)
	if err != nil {
		u.logger.Error("read file failed", zap.String("sourcePath", sourcePath), zap.Error(err))
		return err
	}

	destDir = strings.ReplaceAll(destDir, "\\", "/")

	filesInRemote, err := u.list(destDir)
	if err != nil {
		u.logger.Error("list remote files failed", zap.String("destDir", destDir), zap.Error(err))
		return err
	}

	for _, entry := range entries {
		fullPath := filepath.Join(sourcePath, entry.Name())

		if entry.IsDir() {
			subDir := filepath.Join(destDir, entry.Name())
			subDir = strings.ReplaceAll(subDir, "\\", "/")
			err := u.CreateRemoteDir(subDir)
			if err != nil {
				u.logger.Error("create remote dir failed", zap.String("subDir", subDir), zap.Error(err))
				continue
			}
			err = u.UploadFilesInDirectory(fullPath, subDir)
			if err != nil {
				u.logger.Error("upload files in directory failed", zap.String("fullPath", fullPath), zap.String("subDir", subDir), zap.Error(err))
				continue
			}
		} else {
			exists := u.checkFileExistsInDirectory(entry.Name(), filesInRemote)
			if !exists {
				u.wg.Add(1)
				u.concurrentFiles <- struct{}{}

				go func(file os.DirEntry) {
					defer u.wg.Done()
					defer func() {
						<-u.concurrentFiles
					}()

					err := u.UploadFile(fullPath, destDir)
					if err != nil {
						u.logger.Error("upload failed", zap.String("fullPath", fullPath), zap.Error(err))
						return
					}

					if u.deleteAfterUpload {
						err = os.Remove(fullPath)
						if err != nil {
							u.logger.Error("delete file failed", zap.String("fullPath", fullPath), zap.Error(err))
							return
						}
						u.logger.Info("deleted file", zap.String("fullPath", fullPath))
					}
				}(entry)
			} else {
				fileInfo, err := os.Stat(fullPath)
				if err != nil {
					u.logger.Error("stat for existing file failed", zap.String("fullPath", fullPath), zap.Error(err))
					return err
				}
				u.Progress.AddExisting(float64(fileInfo.Size()))
				u.logger.Info("file in directory exists", zap.String("fullPath", fullPath))
			}
		}
	}

	return nil
}

func (u *UploadService) GetFilesInDirectoryInfo(sourcePath string) (FileInfo, error) {
	entries, err := os.ReadDir(sourcePath)
	if err != nil {
		return FileInfo{}, err
	}

	var info FileInfo

	for _, entry := range entries {
		fullPath := filepath.Join(sourcePath, entry.Name())

		if entry.IsDir() {
			subInfo, err := u.GetFilesInDirectoryInfo(fullPath)
			if err != nil {
				return FileInfo{}, err
			}

			info.TotalFiles += subInfo.TotalFiles
			info.TotalSize += subInfo.TotalSize
		} else {
			info.TotalFiles++
			fileInfo, err := os.Stat(fullPath)
			if err == nil {
				info.TotalSize += fileInfo.Size()
			}
		}
	}

	return info, nil
}

type FileInfo struct {
	TotalFiles int
	TotalSize  int64
}
