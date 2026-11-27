package share

import "time"

// MaxNoteLength is the longest note, in characters, a share may carry.
const MaxNoteLength = 500

// MaxDownloadLimit is the largest download limit a share may have.
const MaxDownloadLimit = 1_000_000

type CreateBody struct {
	Password string `json:"password"`
	Expires  string `json:"expires"`
	Unit     string `json:"unit"`
	// Note is shown to whoever opens the link.
	Note string `json:"note"`
	// MaxDownloads is how many downloads the link allows; 0 means no limit.
	MaxDownloads int `json:"maxDownloads"`
}

// Link is the information needed to build a shareable link.
type Link struct {
	Hash         string `json:"hash" storm:"id,index"`
	Path         string `json:"path" storm:"index"`
	UserID       uint   `json:"userID"`
	Expire       int64  `json:"expire"`
	PasswordHash string `json:"password_hash,omitempty"`
	// Token is a random value that will only be set when PasswordHash is set. It is
	// URL-Safe and is used to download links in password-protected shares via a
	// query arg.
	Token string `json:"token,omitempty"`

	// Note is a message from the owner, shown on the share page.
	Note string `json:"note,omitempty"`
	// Created is when the link was made, as a Unix time. Zero for links made
	// before it was recorded.
	Created int64 `json:"created,omitempty"`
	// MaxDownloads is how many downloads the link allows; 0 means no limit.
	MaxDownloads int `json:"maxDownloads,omitempty"`
	// Downloads counts the downloads made through the link.
	Downloads int `json:"downloads,omitempty"`
}

// Expired reports whether the link's expiry time has passed.
func (l *Link) Expired(now time.Time) bool {
	return l.Expire != 0 && l.Expire <= now.Unix()
}

// Exhausted reports whether the link has used up its download limit.
func (l *Link) Exhausted() bool {
	return l.MaxDownloads > 0 && l.Downloads >= l.MaxDownloads
}

// DownloadsLeft returns how many downloads remain, and false when the link
// has no limit.
func (l *Link) DownloadsLeft() (int, bool) {
	if l.MaxDownloads <= 0 {
		return 0, false
	}
	return max(l.MaxDownloads-l.Downloads, 0), true
}
