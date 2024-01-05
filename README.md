<h1 align="center">Teldrive Upload</h1>

**Concurrently upload file parts using multiple threads for faster uploads. The default is set to 4 threads.**

### How To Use

**Follow the steps below:**
1. Create the `upload.env` file with the following variables:

```shell
API_URL="http://localhost:8080" # URL of hosted app
SESSION_TOKEN="" # User session token, accessible from Teldrive app cookies
PART_SIZE=500M # Same as Rclone Size Format
CHANNEL_ID=0 # Channel ID where files will be saved; if not set, the default will be used as set from the UI
WORKERS=4 # Number of workers to use when uploading multi-parts of a big file; increase for higher speeds with large files (default is 4)
TRANSFERS=4 # Number of files to upload simultaneously (default is 4)
RANDOMISE_PART=true # Set random name to uploaded file (default is true)
ENCRYPT_FILES=false # Encrypt your files using Teldrive encryption (default is false)
DELETE_AFTER_UPLOAD=false # Delete each file immediately after a successful upload (default is false)
DEBUG=false # Enable debug mode to troubleshoot errors (default is false)
```
2. Smaller part sizes result in faster upload speeds.
3. Download the release binary of Teldrive Upload from the releases section.


```shell
./uploader -path "" -dest "" -workers 4 -transfers 4
```

| Option      | Required | Description |
| ----------- | -------- | ----------- |
| `-path`     | Yes      | Here you can pass single file or folder path. |
| `-dest`     | Yes      | Remote output path where files will be saved. |
| `-workers`  | No       | Same as WORKERS. If set, it overrides the value in upload.env. |
| `-transfers`| No       | Same as TRANSFERS. If set, it overrides the value in upload.env. |
