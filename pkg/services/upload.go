package services

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"uploader/pkg/logger"
	"uploader/pkg/types"

	"github.com/gofrs/uuid"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/fserrors"
	"github.com/rclone/rclone/lib/rest"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

var retryErrorCodes = []int{
	429, // Too Many Requests.
	500, // Internal Server Error
	502, // Bad Gateway
	503, // Service Unavailable
	504, // Gateway Timeout
	509, // Bandwidth Limit Exceeded
}

type ProgressReader struct {
	io.Reader
	Reporter func(r int64)
}

func (pr *ProgressReader) Read(p []byte) (n int, err error) {
	n, err = pr.Reader.Read(p)
	pr.Reporter(int64(n))
	return
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
	Progress          *mpb.Progress
	wg                *sync.WaitGroup
}

func NewUploadService(http *rest.Client, numWorkers int, numTransfers int, partSize int64, encryptFiles bool, randomisePart bool, channelID int64, deleteAfterUpload bool, pacer *fs.Pacer, ctx context.Context, progress *mpb.Progress, wg *sync.WaitGroup) *UploadService {
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
	}
}

func shouldRetry(ctx context.Context, resp *http.Response, err error) (bool, error) {
	if fserrors.ContextError(ctx, &err) {
		return false, err
	}
	return fserrors.ShouldRetry(err) || fserrors.ShouldRetryHTTP(resp, retryErrorCodes), err
}

func (u *UploadService) checkFileExists(fileName string, path string) bool {
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
	if resp.StatusCode != 404 && err == nil && len(info.Files) > 0 {
		return true
	}

	return false
}

func (u *UploadService) UploadFile(filePath string, destDir string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	buffer := make([]byte, 512)
	_, err = file.Read(buffer)
	if err != nil {
		logger.Error.Println("Error reading file:", filePath, err)
		return nil
	}

	mimeType := http.DetectContentType(buffer)

	fileInfo, _ := file.Stat()
	fileSize := fileInfo.Size()
	fileName := filepath.Base(filePath)

	if u.checkFileExists(fileName, destDir) {
		logger.Info.Println("file exists:", fileName)
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

	// bar := progressbar.NewOptions64(fileSize,
	// 	progressbar.OptionShowCount(),
	// 	progressbar.OptionSetWriter(os.Stderr),
	// 	progressbar.OptionEnableColorCodes(true),
	// 	progressbar.OptionShowBytes(true),
	// 	progressbar.OptionSetWidth(10),
	// 	progressbar.OptionThrottle(65*time.Millisecond),
	// 	progressbar.OptionSetDescription(fileName),
	// 	progressbar.OptionSetTheme(progressbar.Theme{
	// 		Saucer:        "[green]=[reset]",
	// 		SaucerHead:    "[green]>[reset]",
	// 		SaucerPadding: " ",
	// 		BarStart:      "[",
	// 		BarEnd:        "]",
	// 	}),
	// 	progressbar.OptionFullWidth(),
	// 	progressbar.OptionSetRenderBlankState(true))

	shortenedName := func(name string) string {
		const maxFileNameLength = 75

		if len(fileName) > maxFileNameLength {
			half := maxFileNameLength / 2
			return fileName[:half-2] + "..." + fileName[len(fileName)-half+1:]
		}
		return fileName
	}(fileName)

	var bar *mpb.Bar
	barOptions := []mpb.BarOption{
		mpb.PrependDecorators(
			decor.Name(shortenedName, decor.WC{C: decor.DSyncWidthR | decor.DextraSpace}),
			decor.Name(" ("),
			decor.Percentage(decor.WCSyncSpace, decor.WC{C: decor.DSyncWidthR}),
			decor.Name(")  "),
			decor.Counters(decor.SizeB1000(0), "% .2f/% .2f", decor.WC{C: decor.DSyncWidthR}),
		), mpb.AppendDecorators(
			// decor.EwmaETA(decor.ET_STYLE_GO, 60),
			decor.AverageETA(decor.ET_STYLE_GO),
			decor.Name(" | "),
			// decor.OnComplete(decor.EwmaSpeed(decor.SizeB1000(0), "% .2f", 60, decor.WC{C: decor.DSyncWidthR}), "completed"),
			decor.OnComplete(decor.AverageSpeed(decor.SizeB1000(0), "% .2f", decor.WC{C: decor.DSyncWidthR}), "completed"),
		),
	}

	bar = u.Progress.AddBar(fileSize,
		barOptions...,
	)
	// u.progress = mpb.New(mpb.WithWidth(64))
	// bar = u.progress.New(fileSize,
	// 	mpb.BarStyle().Rbound("|"),
	// 	barOptions...,
	// )

	go func() {
		wg.Wait()
		close(uploadedParts)
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
				logger.Error.Println("Error:", err)
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
				logger.Error.Println("Error:", err)
				return
			}

			pr := &ProgressReader{file, func(r int64) {
				bar.IncrInt64(r)
			}}
			// pr := bar.ProxyReader(file)
			// defer pr.Close()

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
				logger.Error.Println("Error:", err)
				return
			}
			if resp.StatusCode == 201 {
				uploadedParts <- partFile
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
		bar.Abort(true)
		bar.Wait()
		return fmt.Errorf("upload failed: %s", fileName)
	}
	bar.Wait()

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

	if err != nil && resp.StatusCode == 404 {
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
		log.Fatal(err)
	}

	destDir = strings.ReplaceAll(destDir, "\\", "/")

	filesInRemote, err := u.list(destDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		fullPath := filepath.Join(sourcePath, entry.Name())

		if entry.IsDir() {
			subDir := filepath.Join(destDir, entry.Name())
			subDir = strings.ReplaceAll(subDir, "\\", "/")
			err := u.CreateRemoteDir(subDir)
			if err != nil {
				logger.Error.Fatalln(err)
			}
			err = u.UploadFilesInDirectory(fullPath, subDir)
			if err != nil {
				logger.Error.Println(err)
			}
		} else {
			exists := u.checkFileExistsInDirectory(entry.Name(), filesInRemote)
			if !exists {
				u.concurrentFiles <- struct{}{}
				u.wg.Add(1)

				go func(file os.DirEntry) {
					defer u.wg.Done()
					defer func() {
						<-u.concurrentFiles
					}()

					err := u.UploadFile(fullPath, destDir)
					if err != nil {
						logger.Error.Println("upload failed:", err)
						return
					}

					if u.deleteAfterUpload {
						err = os.Remove(fullPath)
						if err != nil {
							logger.Error.Printf("error deleting file \"%s\" %s", fullPath, err)
							return
						}
						// Info.Println("deleted file:", fullPath)
					}
				}(entry)
			} else {
				logger.Info.Println("file exists:", entry.Name())
			}
		}
	}

	return nil
}
