package types

import "time"

type PartFile struct {
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

type CreateFileRequest struct {
	Name      string     `json:"name"`
	Type      string     `json:"type"`
	Path      string     `json:"path,omitempty"`
	MimeType  string     `json:"mimeType,omitempty"`
	Size      int64      `json:"size,omitempty"`
	ChannelID int64      `json:"channelId,omitempty"`
	Encrypted bool       `json:"encrypted,omitempty"`
	Parts     []FilePart `json:"parts,omitempty"`
	ParentId  string     `json:"parentId,omitempty"`
	ModTime   time.Time  `json:"updatedAt,omitempty"`
}

// MetadataRequestOptions represents all the options when listing folder contents
type MetadataRequestOptions struct {
	Page  int64
	Limit int64
}

// FileInfo represents a file when listing folder contents
type FileInfo struct {
	Id       string    `json:"id"`
	Name     string    `json:"name"`
	MimeType string    `json:"mimeType"`
	Size     int64     `json:"size"`
	ParentId string    `json:"parentId"`
	Type     string    `json:"type"`
	ModTime  time.Time `json:"updatedAt"`
}

type Meta struct {
	Count       int `json:"count,omitempty"`
	TotalPages  int `json:"totalPages,omitempty"`
	CurrentPage int `json:"currentPage,omitempty"`
}

// ReadMetadataResponse is the response when listing folder contents
type ReadMetadataResponse struct {
	Files []FileInfo `json:"items"`
	Meta  Meta       `json:"meta"`
}
