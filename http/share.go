package fbhttp

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	fberrors "github.com/yllix/stowr/v3/errors"
	"github.com/yllix/stowr/v3/share"
	"github.com/yllix/stowr/v3/users"
	"golang.org/x/crypto/bcrypt"
)

// shareResponse is the client-facing representation of a share. It deliberately
// omits the server-side secrets of share.Link — the bcrypt PasswordHash (which
// would be crackable offline) and the bypass Token — exposing only whether the
// share is password-protected via HasPassword.
type shareResponse struct {
	Hash         string `json:"hash"`
	Path         string `json:"path"`
	UserID       uint   `json:"userID"`
	Expire       int64  `json:"expire"`
	HasPassword  bool   `json:"hasPassword"`
	Note         string `json:"note,omitempty"`
	Created      int64  `json:"created,omitempty"`
	MaxDownloads int    `json:"maxDownloads,omitempty"`
	Downloads    int    `json:"downloads"`
}

func toShareResponse(l *share.Link) *shareResponse {
	return &shareResponse{
		Hash:         l.Hash,
		Path:         l.Path,
		UserID:       l.UserID,
		Expire:       l.Expire,
		HasPassword:  l.PasswordHash != "",
		Note:         l.Note,
		Created:      l.Created,
		MaxDownloads: l.MaxDownloads,
		Downloads:    l.Downloads,
	}
}

// shareHashBytes is how many random bytes make up a share link's ID: 96 bits,
// so links can't be found by guessing. Links made before this was raised
// from 6 bytes keep working.
const shareHashBytes = 12

// maxShareDuration is the longest a share may be set to last.
const maxShareDuration = 100 * 365 * 24 * time.Hour

// shareDuration turns the expiry fields of a create request into a duration;
// zero means the share never expires.
func shareDuration(expires, unit string) (time.Duration, error) {
	if expires == "" {
		return 0, nil
	}
	num, err := strconv.ParseInt(expires, 10, 64)
	if err != nil || num < 0 {
		return 0, fmt.Errorf("invalid expiry %q", expires)
	}

	var per time.Duration
	switch unit {
	case "seconds":
		per = time.Second
	case "minutes":
		per = time.Minute
	case "days":
		per = 24 * time.Hour
	default:
		per = time.Hour
	}
	if num > int64(maxShareDuration/per) {
		return 0, fmt.Errorf("expiry is too far away")
	}
	return time.Duration(num) * per, nil
}

func toShareResponses(links []*share.Link) []*shareResponse {
	res := make([]*shareResponse, 0, len(links))
	for _, l := range links {
		res = append(res, toShareResponse(l))
	}
	return res
}

func withPermShare(fn handleFunc) handleFunc {
	return withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
		if !d.user.Perm.Share || !d.user.Perm.Download {
			return http.StatusForbidden, nil
		}

		return fn(w, r, d)
	})
}

var shareListHandler = withPermShare(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	var (
		s   []*share.Link
		err error
	)
	if d.user.Perm.Admin {
		s, err = d.store.Share.All()
	} else {
		s, err = d.store.Share.FindByUserID(d.user.ID)
	}
	if errors.Is(err, fberrors.ErrNotExist) {
		return renderJSON(w, r, []*shareResponse{})
	}

	if err != nil {
		return http.StatusInternalServerError, err
	}

	sort.Slice(s, func(i, j int) bool {
		if s[i].UserID != s[j].UserID {
			return s[i].UserID < s[j].UserID
		}
		return s[i].Expire < s[j].Expire
	})

	return renderJSON(w, r, toShareResponses(s))
})

var shareGetsHandler = withPermShare(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	var (
		s   []*share.Link
		err error
	)
	if d.user.Perm.Admin {
		s, err = getSharesForAdminPath(d, r.URL.Path)
	} else {
		s, err = d.store.Share.Gets(r.URL.Path, d.user.ID)
	}
	if errors.Is(err, fberrors.ErrNotExist) {
		return renderJSON(w, r, []*shareResponse{})
	}

	if err != nil {
		return http.StatusInternalServerError, err
	}

	return renderJSON(w, r, toShareResponses(s))
})

func getSharesForAdminPath(d *data, path string) ([]*share.Link, error) {
	links, err := d.store.Share.All()
	if err != nil {
		return nil, err
	}

	adminPath := filepath.Clean(d.user.FullPath(path))
	owners := make(map[uint]*users.User)
	filtered := make([]*share.Link, 0, len(links))
	for _, link := range links {
		owner, ok := owners[link.UserID]
		if !ok {
			owner, err = d.store.Users.Get(d.server.Root, d.server.FollowExternalSymlinks, link.UserID)
			if err != nil && !errors.Is(err, fberrors.ErrNotExist) {
				return nil, err
			}
			owners[link.UserID] = owner // owner is nil on ErrNotExist
		}
		if owner != nil && filepath.Clean(owner.FullPath(link.Path)) == adminPath {
			filtered = append(filtered, link)
		}
	}

	return filtered, nil
}

var shareDeleteHandler = withPermShare(func(_ http.ResponseWriter, r *http.Request, d *data) (int, error) {
	hash := strings.TrimSuffix(r.URL.Path, "/")
	hash = strings.TrimPrefix(hash, "/")

	if hash == "" {
		return http.StatusBadRequest, nil
	}

	link, err := d.store.Share.GetByHash(hash)
	if err != nil {
		return errToStatus(err), err
	}

	if link.UserID != d.user.ID && !d.user.Perm.Admin {
		return http.StatusForbidden, nil
	}

	err = d.store.Share.Delete(hash)
	return errToStatus(err), err
})

var sharePostHandler = withPermShare(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	// Only allow sharing paths that currently exist. Otherwise a share could be
	// created for a non-existent path and would silently start exposing
	// whatever file later appears there.
	//
	// d.user.Fs is scoped, so Stat also refuses to follow a symlink whose target
	// escapes the user's scope: that returns a permission error here and so
	// blocks creating a share that points out of scope.
	if _, err := d.user.Fs.Stat(r.URL.Path); err != nil {
		return errToStatus(err), err
	}

	var s *share.Link
	var body share.CreateBody
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return http.StatusBadRequest, fmt.Errorf("failed to decode body: %w", err)
		}
		defer r.Body.Close()
	}

	add, err := shareDuration(body.Expires, body.Unit)
	if err != nil {
		return http.StatusBadRequest, err
	}
	var expire int64
	if add > 0 {
		expire = time.Now().Add(add).Unix()
	}

	note := strings.TrimSpace(body.Note)
	if utf8.RuneCountInString(note) > share.MaxNoteLength {
		return http.StatusBadRequest, fmt.Errorf("the note is longer than %d characters", share.MaxNoteLength)
	}
	if body.MaxDownloads < 0 || body.MaxDownloads > share.MaxDownloadLimit {
		return http.StatusBadRequest, fmt.Errorf("invalid download limit %d", body.MaxDownloads)
	}

	bytes := make([]byte, shareHashBytes)
	if _, err := rand.Read(bytes); err != nil {
		return http.StatusInternalServerError, err
	}
	str := base64.RawURLEncoding.EncodeToString(bytes)

	hash, status, err := getSharePasswordHash(body)
	if err != nil {
		return status, err
	}

	var token string
	if len(hash) > 0 {
		tokenBuffer := make([]byte, 96)
		if _, err := rand.Read(tokenBuffer); err != nil {
			return http.StatusInternalServerError, err
		}
		token = base64.URLEncoding.EncodeToString(tokenBuffer)
	}

	s = &share.Link{
		Path:         r.URL.Path,
		Hash:         str,
		Expire:       expire,
		UserID:       d.user.ID,
		PasswordHash: string(hash),
		Token:        token,
		Note:         note,
		Created:      time.Now().Unix(),
		MaxDownloads: body.MaxDownloads,
	}

	if err := d.store.Share.Save(s); err != nil {
		return http.StatusInternalServerError, err
	}

	return renderJSON(w, r, toShareResponse(s))
})

func getSharePasswordHash(body share.CreateBody) (data []byte, statuscode int, err error) {
	if body.Password == "" {
		return nil, 0, nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("failed to hash password: %w", err)
	}

	return hash, 0, nil
}
