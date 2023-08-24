<h1 align="center">Teldrive Upload</h1>

**Upload file parts concurrentlly  in multiple threads to get faster uploads.Default is 4.**
### How To Use

**Follow Below Steps**
- Create the `upload.env` file with variables given below

```shell
API_URL="http://localhost:8000"
SESSION_TOKEN="" #user session token which can be fetched from teldrive app from cookies
PART_SIZE=1GB # ALLowed sizes 500M,1GB,2GB (1GB is defualt)
```
- Download release binary of teldrive upload from releases section.

```shell
./uploader -path "" -destination "" -ext ".txt"
```

- **-path**  here you can pass single file or folder path.
- **-destination** is output path where fiels will  be saved.
- **-ext**  will upload only matched extension  otherwise leave if you wanna upload all files in folder.