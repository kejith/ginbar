package model

import "time"

type ContentFilter int16

const (
	FilterSFW ContentFilter = iota
	FilterNSFP
	FilterNSFW
	FilterSecret
)

func (f ContentFilter) Valid() bool { return f >= FilterSFW && f <= FilterSecret }

type MediaKind int16

const (
	MediaImage MediaKind = iota
	MediaVideo
)

type PostVote int16

const (
	VoteDown    PostVote = -1
	VoteNeutral PostVote = 0
	VoteUp      PostVote = 1
)

func (v PostVote) Valid() bool { return v == VoteDown || v == VoteNeutral || v == VoteUp }

type PostSummary struct {
	ID        int64         `json:"id"`
	AuthorID  int64         `json:"authorId"`
	Filter    ContentFilter `json:"filter"`
	Score     int32         `json:"score"`
	UserVote  PostVote      `json:"userVote"`
	CreatedAt time.Time     `json:"createdAt"`
	Media     MediaSummary  `json:"media"`
}

type MediaSummary struct {
	Kind       MediaKind `json:"kind"`
	StorageKey string    `json:"storageKey"`
	MIMEType   string    `json:"mimeType"`
	Width      int32     `json:"width"`
	Height     int32     `json:"height"`
	DurationMS int64     `json:"durationMs,omitempty"`
}
