package types

type PartFile struct {
	Name      string `json:"name"`
	PartId    int    `json:"partId"`
	PartNo    int    `json:"partNo"`
	Size      int64  `json:"size"`
	ChannelID int64  `json:"channelId"`
	Encrypted bool   `json:"encrypted"`
	Salt      string `json:"salt"`
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
