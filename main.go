package main

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
	"time"

	"flag"

	"github.com/gofrs/uuid"
	"github.com/kelseyhightower/envconfig"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/fserrors"
	"github.com/rclone/rclone/lib/pacer"
	"github.com/rclone/rclone/lib/rest"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"

	"github.com/joho/godotenv"
)

var Info = log.New(os.Stdout, "\u001b[34mINFO: \u001B[0m", log.LstdFlags|log.Lshortfile)

var Warning = log.New(os.Stdout, "\u001b[33mWARNING: \u001B[0m", log.LstdFlags|log.Lshortfile)

var Error = log.New(os.Stdout, "\u001b[31mERROR: \u001b[0m", log.LstdFlags|log.Lshortfile)

var Debug = log.New(os.Stdout, "\u001b[36mDEBUG: \u001B[0m", log.LstdFlags|log.Lshortfile)

type Config struct {
	ApiURL        string        `envconfig:"API_URL" required:"true"`
	SessionToken  string        `envconfig:"SESSION_TOKEN" required:"true"`
	PartSize      fs.SizeSuffix `envconfig:"PART_SIZE"`
	Workers       int           `envconfig:"WORKERS" default:"4"`
	Transfers     int           `envconfig:"TRANSFERS" default:"4"`
	RandomisePart bool          `envconfig:"RANDOMISE_PART" default:"true"`
	EncryptFiles  bool          `envconfig:"ENCRYPT_FILES" default:"false"`
	ChannelID     int64         `envconfig:"CHANNEL_ID"`
}

type PartFile struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	PartId     int    `json:"partId"`
	PartNo     int    `json:"partNo"`
	TotalParts int    `json:"totalParts"`
	Size       int64  `json:"size"`
	ChannelID  int64  `json:"channelId"`
	Encrypted  bool   `json:"encrypted"`
	Salt       string `json:"salt"`
}

type FilePart struct {
	ID     int64  `json:"id"`
	PartNo int    `json:"partNo"`
	Salt   string `json:"salt"`
}

type UploadFile struct {
	Parts []PartFile `json:"parts,omitempty"`
}

type FilePayload struct {
	Name      string     `json:"name"`
	Type      string     `json:"type"`
	Parts     []FilePart `json:"parts,omitempty"`
	MimeType  string     `json:"mimeType"`
	Path      string     `json:"path"`
	Size      int64      `json:"size"`
	ChannelID int64      `json:"channelId"`
	Encrypted bool       `json:"encrypted"`
}

type CreateDirRequest struct {
	Path string `json:"path"`
}

type MetadataRequestOptions struct {
	PerPage       uint64
	SearchField   string
	Search        string
	NextPageToken string
}

// FileInfo represents a file when listing folder contents
type FileInfo struct {
	Id       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size"`
	ParentId string `json:"parentId"`
	Type     string `json:"type"`
	ModTime  string `json:"updatedAt"`
}

// ReadMetadataResponse is the response when listing folder contents
type ReadMetadataResponse struct {
	Files         []FileInfo `json:"results"`
	NextPageToken string     `json:"nextPageToken,omitempty"`
}

type Uploader struct {
	http            *rest.Client
	numWorkers      int
	concurrentFiles chan struct{}
	partSize        int64
	encryptFiles    bool
	randomisePart   bool
	channelID       int64
	pacer           *fs.Pacer
	ctx             context.Context
	progress        *mpb.Progress
	wg              *sync.WaitGroup
}

var retryErrorCodes = []int{
	429, // Too Many Requests.
	500, // Internal Server Error
	502, // Bad Gateway
	503, // Service Unavailable
	504, // Gateway Timeout
	509, // Bandwidth Limit Exceeded
}

func shouldRetry(ctx context.Context, resp *http.Response, err error) (bool, error) {
	if fserrors.ContextError(ctx, &err) {
		return false, err
	}
	return fserrors.ShouldRetry(err) || fserrors.ShouldRetryHTTP(resp, retryErrorCodes), err
}

func loadConfigFromEnv() (*Config, error) {

	var config Config

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

type ProgressReader struct {
	io.Reader
	Reporter func(r int64)
}

func (pr *ProgressReader) Read(p []byte) (n int, err error) {
	n, err = pr.Reader.Read(p)
	pr.Reporter(int64(n))
	return
}

func (u *Uploader) checkFileExists(fileName string, path string) bool {
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
	var info ReadMetadataResponse
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

func (u *Uploader) uploadFile(filePath string, destDir string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	buffer := make([]byte, 512)
	_, err = file.Read(buffer)
	if err != nil {
		Error.Println("Error reading file:", filePath, err)
		return nil
	}

	mimeType := http.DetectContentType(buffer)

	fileInfo, _ := file.Stat()
	fileSize := fileInfo.Size()
	fileName := filepath.Base(filePath)

	if u.checkFileExists(fileName, destDir) {
		Info.Println("file exists:", fileName)
		return nil
	}

	input := fmt.Sprintf("%s:%s:%d", fileName, destDir, fileSize)

	hash := md5.Sum([]byte(input))
	hashString := hex.EncodeToString(hash[:])

	uploadURL := fmt.Sprintf("/api/uploads/%s", hashString)

	var existingParts map[int]PartFile
	var uploadFile UploadFile

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
			existingParts = make(map[int]PartFile, len(uploadFile.Parts))
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

	uploadedParts := make(chan PartFile, totalParts)
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

	bar = u.progress.AddBar(fileSize,
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

		concurrentWorkers <- struct{}{}
		wg.Add(1)

		go func(partNumber int64, start, end int64) {
			defer wg.Done()
			defer func() {
				<-concurrentWorkers
			}()

			file, err := os.Open(filePath)
			if err != nil {
				Error.Println("Error:", err)
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
				Error.Println("Error:", err)
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

			var partFile PartFile
			resp, err := u.http.CallJSON(context.TODO(), &opts, nil, &partFile)

			if err != nil {
				Error.Println("Error:", err)
				return
			}
			if resp.StatusCode == 201 {
				uploadedParts <- partFile
			}
		}(i, start, end)
	}

	var parts []FilePart
	for uploadPart := range uploadedParts {
		if uploadPart.PartId != 0 {
			parts = append(parts, FilePart{ID: int64(uploadPart.PartId), PartNo: uploadPart.PartNo, Salt: uploadPart.Salt})
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

	filePayload := FilePayload{
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
func (u *Uploader) createRemoteDir(path string) error {
	opts := rest.Opts{
		Method: "POST",
		Path:   "/api/files/directories",
	}

	if len(path) == 0 || path[0] != '/' {
		path = "/" + path
	}

	mkdir := CreateDirRequest{
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

func (u *Uploader) readMetaDataForPath(path string, options *MetadataRequestOptions) (*ReadMetadataResponse, error) {

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
	var info ReadMetadataResponse
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

func (u *Uploader) list(path string) (files []FileInfo, err error) {

	var limit uint64 = 500
	var nextPageToken string = ""
	for {
		opts := &MetadataRequestOptions{
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

func (u *Uploader) checkFileExistsInDirectory(name string, files []FileInfo) bool {
	for _, item := range files {
		if item.Name == name {
			return true
		}
	}
	return false
}

func (u *Uploader) uploadFilesInDirectory(sourcePath string, destDir string) error {
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
			err := u.createRemoteDir(subDir)
			if err != nil {
				Error.Fatalln(err)
			}
			err = u.uploadFilesInDirectory(fullPath, subDir)
			if err != nil {
				Error.Println(err)
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

					err := u.uploadFile(fullPath, destDir)
					if err != nil {
						Error.Println("upload failed:", err)
					}
				}(entry)
			} else {
				Info.Println("file exists:", entry.Name())
			}
		}
	}

	return nil
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
		Error.Fatalln(err)
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
		Error.Fatalln("transfers flag must be a number", err)
	}
	concurrentFiles := make(chan struct{}, numTransfers)

	numWorkers := config.Workers
	if *workers != "" {
		numWorkers, err = strconv.Atoi(*workers)
	}
	if err != nil {
		Error.Fatalln("workers flag must be a number", err)
	}

	uploader := &Uploader{
		http:            httpClient,
		numWorkers:      numWorkers,
		concurrentFiles: concurrentFiles,
		encryptFiles:    config.EncryptFiles,
		randomisePart:   config.RandomisePart,
		channelID:       config.ChannelID,
		partSize:        int64(config.PartSize),
		pacer:           pacer,
		ctx:             ctx,
		progress:        progress,
		wg:              &wg,
	}

	err = uploader.createRemoteDir(*destDir)

	if err != nil {
		Error.Fatalln(err)
	}

	if fileInfo, err := os.Stat(*sourcePath); err == nil {
		if fileInfo.IsDir() {
			err := uploader.uploadFilesInDirectory(*sourcePath, *destDir)
			if err != nil {
				Error.Println("upload failed:", err)
			}
			uploader.progress.Wait()
		} else {
			if err := uploader.uploadFile(*sourcePath, *destDir); err != nil {
				Error.Println("upload failed:", err)
			}
		}
	} else {
		Error.Fatalln(err)
	}

	Info.Println("Uploads complete!")
}
