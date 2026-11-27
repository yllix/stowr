package fbhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asdine/storm/v3"
	"github.com/spf13/afero"

	"github.com/yllix/stowr/v3/files"
	"github.com/yllix/stowr/v3/settings"
	"github.com/yllix/stowr/v3/share"
	"github.com/yllix/stowr/v3/storage"
	"github.com/yllix/stowr/v3/storage/bolt"
	"github.com/yllix/stowr/v3/users"
)

// createShare posts a share for /file.txt and returns the response.
func createShare(t *testing.T, body string) (*httptest.ResponseRecorder, *storage.Storage) {
	t.Helper()
	root := t.TempDir()
	userScope := filepath.Join(root, "user")
	if err := os.MkdirAll(userScope, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userScope, "file.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := []byte("test-signing-key")
	perm := users.Permissions{Share: true, Download: true}
	st := scopedUserStorage(t, userScope, perm, key)

	req, _ := http.NewRequest(http.MethodPost, "/file.txt", strings.NewReader(body))
	req.Header.Set("X-Auth", signToken(t, perm, key))
	rec := httptest.NewRecorder()
	handle(sharePostHandler, "", st, &settings.Server{Root: root}).ServeHTTP(rec, req)
	return rec, st
}

func TestSharePostStoresNoteAndLimit(t *testing.T) {
	rec, st := createShare(t, `{"note":"  For the trip photos  ","maxDownloads":3,"expires":"2","unit":"days"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp shareResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Note != "For the trip photos" || resp.MaxDownloads != 3 || resp.Downloads != 0 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.Created == 0 || resp.Created > time.Now().Unix() {
		t.Fatalf("created = %d, want the creation time", resp.Created)
	}
	if want := time.Now().Add(48 * time.Hour).Unix(); resp.Expire < want-5 || resp.Expire > want+5 {
		t.Fatalf("expire = %d, want about %d", resp.Expire, want)
	}
	// 12 random bytes, unpadded base64: 16 characters.
	if len(resp.Hash) != 16 || strings.ContainsAny(resp.Hash, "=+/") {
		t.Fatalf("hash %q is not a 16-character URL-safe ID", resp.Hash)
	}

	stored, err := st.Share.GetByHash(resp.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Note != resp.Note || stored.MaxDownloads != 3 {
		t.Fatalf("not persisted: %+v", stored)
	}
}

func TestSharePostRejectsBadInput(t *testing.T) {
	for name, body := range map[string]string{
		"non-numeric expiry": `{"expires":"soon"}`,
		"negative expiry":    `{"expires":"-1","unit":"hours"}`,
		"overflowing expiry": `{"expires":"9223372036854775807","unit":"days"}`,
		"negative limit":     `{"maxDownloads":-1}`,
		"huge limit":         `{"maxDownloads":1000000000}`,
		"long note":          fmt.Sprintf(`{"note":%q}`, strings.Repeat("é", share.MaxNoteLength+1)),
	} {
		t.Run(name, func(t *testing.T) {
			rec, _ := createShare(t, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}

	// A note of exactly the limit, counted in characters, is fine.
	rec, _ := createShare(t, fmt.Sprintf(`{"note":%q}`, strings.Repeat("é", share.MaxNoteLength)))
	if rec.Code != http.StatusOK {
		t.Fatalf("note at the limit: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// publicShareStorage stores link and a user owning /file.txt.
func publicShareStorage(t *testing.T, link *share.Link) *storage.Storage {
	t.Helper()
	db, err := storm.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := bolt.NewStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Users.Save(&users.User{
		Username: "owner",
		Password: "pw",
		Perm:     users.Permissions{Share: true, Download: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Settings.Save(&settings.Settings{Key: []byte("key")}); err != nil {
		t.Fatal(err)
	}
	if err := st.Share.Save(link); err != nil {
		t.Fatal(err)
	}
	// A real directory: the symlink-confining filesystem resolves paths on disk.
	fs := files.NewScopedFs(afero.NewOsFs(), t.TempDir())
	if err := afero.WriteFile(fs, "/file.txt", []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	st.Users = &customFSUser{Store: st.Users, fs: fs}
	return st
}

func publicGet(t *testing.T, st *storage.Storage, h handleFunc, target string, modify ...func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, target, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range modify {
		m(req)
	}
	rec := httptest.NewRecorder()
	handle(h, "", st, &settings.Server{}).ServeHTTP(rec, req)
	return rec
}

func TestPublicDownloadLimit(t *testing.T) {
	st := publicShareStorage(t, &share.Link{Hash: "lim", UserID: 1, Path: "/file.txt", MaxDownloads: 2})

	// Viewing in the browser and continuing a started download don't count.
	if rec := publicGet(t, st, publicDlHandler, "lim?inline=true"); rec.Code != http.StatusOK {
		t.Fatalf("inline view: %d", rec.Code)
	}
	if rec := publicGet(t, st, publicDlHandler, "lim", func(r *http.Request) {
		r.Header.Set("Range", "bytes=2-")
	}); rec.Code != http.StatusPartialContent {
		t.Fatalf("range continuation: %d", rec.Code)
	}

	for i := range 2 {
		rec := publicGet(t, st, publicDlHandler, "lim")
		if rec.Code != http.StatusOK || rec.Body.String() != "hello" {
			t.Fatalf("download %d: %d %q", i+1, rec.Code, rec.Body.String())
		}
	}

	// The limit is used up: downloading and opening the share both say so.
	if rec := publicGet(t, st, publicDlHandler, "lim"); rec.Code != http.StatusGone {
		t.Fatalf("download past the limit: expected 410, got %d", rec.Code)
	}
	if rec := publicGet(t, st, publicShareHandler, "lim"); rec.Code != http.StatusGone {
		t.Fatalf("share page past the limit: expected 410, got %d", rec.Code)
	}

	stored, err := st.Share.GetByHash("lim")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Downloads != 2 {
		t.Fatalf("Downloads = %d, want 2", stored.Downloads)
	}
}

func TestPublicDownloadCountsUnlimitedLinks(t *testing.T) {
	st := publicShareStorage(t, &share.Link{Hash: "free", UserID: 1, Path: "/file.txt"})
	for range 3 {
		if rec := publicGet(t, st, publicDlHandler, "free"); rec.Code != http.StatusOK {
			t.Fatalf("download: %d", rec.Code)
		}
	}
	stored, err := st.Share.GetByHash("free")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Downloads != 3 {
		t.Fatalf("Downloads = %d, want 3", stored.Downloads)
	}
}

func TestPublicShareShowsNoteAndLimit(t *testing.T) {
	expire := time.Now().Add(time.Hour).Unix()
	st := publicShareStorage(t, &share.Link{
		Hash: "info", UserID: 1, Path: "/file.txt",
		Note: "Hi!", Expire: expire, MaxDownloads: 5, Downloads: 2,
	})

	rec := publicGet(t, st, publicShareHandler, "info")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Name  string          `json:"name"`
		Share publicShareInfo `json:"share"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Name != "file.txt" {
		t.Fatalf("file fields missing from response: %s", rec.Body.String())
	}
	if resp.Share.Note != "Hi!" || resp.Share.Expire != expire || resp.Share.DownloadsLeft == nil || *resp.Share.DownloadsLeft != 3 {
		t.Fatalf("unexpected share info: %+v", resp.Share)
	}
	for _, secret := range []string{"password_hash", "maxDownloads", "downloads\""} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("public response exposes %s: %s", secret, rec.Body.String())
		}
	}
}

func TestPublicSharePasswordGuessingIsLimited(t *testing.T) {
	const passwordBcrypt = "$2y$10$TFAmdCbyd/mEZDe5fUeZJu.MaJQXRTwdqb/IQV.eTn6dWrF58gCSe" // "password"
	st := publicShareStorage(t, &share.Link{Hash: "guess", UserID: 1, Path: "/file.txt", PasswordHash: passwordBcrypt, Token: "tok"})
	from := func(addr, password string) func(*http.Request) {
		return func(r *http.Request) {
			r.RemoteAddr = addr
			r.Header.Set("X-SHARE-PASSWORD", password)
		}
	}

	for i := range 10 {
		if rec := publicGet(t, st, publicShareHandler, "guess", from("203.0.113.7:4000", "wrong")); rec.Code != http.StatusUnauthorized {
			t.Fatalf("guess %d: expected 401, got %d", i+1, rec.Code)
		}
	}
	// Blocked now, even with the right password and a spoofed forwarding header.
	rec := publicGet(t, st, publicShareHandler, "guess", from("203.0.113.7:4001", "password"), func(r *http.Request) {
		r.Header.Set("X-Forwarded-For", "198.51.100.1")
	})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("after 10 wrong passwords: expected 429, got %d", rec.Code)
	}
	// Other addresses, and the link's download token, aren't affected.
	if rec := publicGet(t, st, publicShareHandler, "guess", from("192.0.2.9:4000", "password")); rec.Code != http.StatusOK {
		t.Fatalf("another address: expected 200, got %d", rec.Code)
	}
	if rec := publicGet(t, st, publicShareHandler, "guess?token=tok", from("203.0.113.7:4002", "")); rec.Code != http.StatusOK {
		t.Fatalf("token: expected 200, got %d", rec.Code)
	}
}

func TestAttemptLimiterWindow(t *testing.T) {
	now := time.Unix(0, 0)
	l := newAttemptLimiter(2, time.Minute)
	l.now = func() time.Time { return now }

	l.fail("k")
	l.fail("k")
	if !l.blocked("k") {
		t.Fatal("expected k blocked after 2 failures")
	}
	if l.blocked("other") {
		t.Fatal("other keys must not be blocked")
	}
	now = now.Add(time.Minute)
	if l.blocked("k") {
		t.Fatal("expected the block to lift after the window")
	}
	if len(l.attempts) != 0 {
		t.Fatalf("stale entries kept: %d", len(l.attempts))
	}
	l.fail("k")
	l.reset("k")
	if l.blocked("k") || len(l.attempts) != 0 {
		t.Fatal("reset must clear the key")
	}
}
