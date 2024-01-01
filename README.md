<h1 align="center">Teldrive Upload</h1>

**Upload file parts concurrentlly  in multiple threads to get faster uploads.Default is 4.**
### How To Use

**Follow Below Steps**
- Create the `upload.env` file with variables given below

```shell
API_URL="http://localhost:8080" # URL of hosted app
SESSION_TOKEN="" # User session token, obtainable from Teldrive app cookies
PART_SIZE=500M # Same as Rclone Size Format
CHANNEL_ID=0 # Channel ID where files will be saved; if not set, the default will be used as set from the UI
WORKERS=4 # Number of workers to use when uploading multi-parts of a big file; increase for higher speeds with large files (default is 4)
TRANSFERS=4 # Number of files to upload simultaneously (default is 4)
RANDOMISE_PART=true # Set random name to uploaded file (default is true)
ENCRYPT_FILES=false # Encrypt your files using Teldrive encryption (default is false)
DELETE_AFTER_UPLOAD=false # Delete each file immediately after a successful upload

```
- Smaller part size will give max upload speed.
- Download release binary of teldrive upload from releases section.

```shell
./uploader -path "" -dest "" -workers 4 -transfers 4
```

| Option      | Required | Description |
| ----------- | -------- | ----------- |
| `-path`     | Yes      | Here you can pass single file or folder path. |
| `-dest`     | Yes      | Remote output path where files will be saved. |
| `-workers`  | No       | Same as WORKERS. If set, it overrides the value in upload.env. |
| `-transfers`| No       | Same as TRANSFERS. If set, it overrides the value in upload.env. |
