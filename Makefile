.PHONY: uploader
uploader:
	go build -trimpath -ldflags "-s -w -extldflags=-static" -o uploader.exe main.go

.PHONY: uploader-gc
uploader-gc:
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -extldflags=-static" -o bin/uploader main.go