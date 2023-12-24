<h1 align="center">Teldrive Upload</h1>

**Upload file parts concurrentlly  in multiple threads to get faster uploads.Default is 4.**
### How To Use

**Follow Below Steps**
- Create the `upload.env` file with variables given below

```shell
API_URL="http://localhost:8000" # url of hosted app
SESSION_TOKEN="" #user session token which can be fetched from teldrive app from cokies
PART_SIZE=1000M # Same as Rclone Size Format
CHANNEL_ID=0 # Channel ID where files will be saved if not set default will be used which is set from UI
WORKERS=4 # Number of current workers to use when uploading multi-parts of a big file, increase this to attain higher speeds with large files (4 is default)
TRANSFERS=4 # Number of current files to upload at the same time. (default 4)
RANDOMISE_PART=true # Set random name to uploaded file (default true)
ENCRYPT_FILES=false # Encrypt your files using teldrive encryption (default false)
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
