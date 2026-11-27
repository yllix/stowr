package fbhttp

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/yllix/stowr/v3/users"
)

// resume sends the resume request: the bytes just before offset as proof.
func (f *tusTestFixture) resume(t *testing.T, name string, length, offset int, sample []byte) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/api/tus/"+name+"?override=false&resume=true", bytes.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Auth", f.token)
	req.Header.Set("Upload-Length", strconv.Itoa(length))
	req.Header.Set("Stowr-Resume-Offset", strconv.Itoa(offset))

	res, err := f.client.Do(req)
	if err != nil {
		t.Fatalf("POST resume: %v", err)
	}
	return res
}

func (f *tusTestFixture) head(t *testing.T, url string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodHead, url, http.NoBody)
	req.Header.Set("X-Auth", f.token)
	res, err := f.client.Do(req)
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	res.Body.Close()
	return res
}

// An upload interrupted when the server restarted (so its upload cache is
// gone) leaves a partial file. Resuming it continues from where it stopped
// and produces the complete, byte-identical file.
func TestTusResumeAfterServerForgotUpload(t *testing.T) {
	const fileSize = 3*resumeSampleMax + 777
	const partial = 2*resumeSampleMax + 123

	f := newTusTestFixture(t)
	payload := testPayload(fileSize)

	// What an earlier, interrupted upload left behind.
	dest := filepath.Join(f.scope, "big.iso")
	if err := os.WriteFile(dest, payload[:partial], 0o644); err != nil {
		t.Fatal(err)
	}

	res := f.resume(t, "big.iso", fileSize, partial, payload[partial-resumeSampleMax:partial])
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("resume: status %d body %q", res.StatusCode, body)
	}
	if got := res.Header.Get("Upload-Offset"); got != strconv.Itoa(partial) {
		t.Fatalf("resume: Upload-Offset = %q, want %d", got, partial)
	}
	uploadURL := f.srv.URL + res.Header.Get("Location")

	// The tus client asks where to continue.
	if h := f.head(t, uploadURL); h.Header.Get("Upload-Offset") != strconv.Itoa(partial) || h.Header.Get("Upload-Length") != strconv.Itoa(fileSize) {
		t.Fatalf("HEAD: offset %q length %q", h.Header.Get("Upload-Offset"), h.Header.Get("Upload-Length"))
	}

	patch, _ := f.patch(t, uploadURL, partial, payload[partial:])
	_, _ = io.Copy(io.Discard, patch.Body)
	patch.Body.Close()
	if patch.StatusCode != http.StatusNoContent {
		t.Fatalf("PATCH remainder: status %d", patch.StatusCode)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("resumed file differs from the source (len %d, want %d)", len(got), len(payload))
	}
}

// A same-named file whose bytes differ must not be appended to: that would
// silently corrupt it.
func TestTusResumeRejectsDifferentPartialFile(t *testing.T) {
	const fileSize = 200_000
	const partial = 120_000

	f := newTusTestFixture(t)
	payload := testPayload(fileSize)

	other := make([]byte, partial)
	copy(other, payload[:partial])
	other[partial-10] ^= 0xff // differs inside the sample
	dest := filepath.Join(f.scope, "doc.bin")
	if err := os.WriteFile(dest, other, 0o644); err != nil {
		t.Fatal(err)
	}

	res := f.resume(t, "doc.bin", fileSize, partial, payload[partial-4096:partial])
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusConflict || string(body) != errResumeMismatch {
		t.Fatalf("status %d body %q, want 409 mismatch", res.StatusCode, body)
	}
	if got, _ := os.ReadFile(dest); !bytes.Equal(got, other) {
		t.Fatal("partial file was modified by a rejected resume")
	}
}

func TestTusResumeRejectsSizeMismatch(t *testing.T) {
	f := newTusTestFixture(t)
	payload := testPayload(50_000)
	if err := os.WriteFile(filepath.Join(f.scope, "a.bin"), payload[:30_000], 0o644); err != nil {
		t.Fatal(err)
	}

	// Client believes 20,000 bytes are on the server; there are 30,000.
	res := f.resume(t, "a.bin", 50_000, 20_000, payload[10_000:20_000])
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("status %d, want 409", res.StatusCode)
	}

	// Nothing left to resume: the server file is already full length.
	if err := os.WriteFile(filepath.Join(f.scope, "b.bin"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	res = f.resume(t, "b.bin", 50_000, 50_000, payload[40_000:50_000])
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("complete file: status %d, want 409", res.StatusCode)
	}
}

func TestTusResumeRejectsBadSample(t *testing.T) {
	f := newTusTestFixture(t)
	payload := testPayload(10_000)
	if err := os.WriteFile(filepath.Join(f.scope, "c.bin"), payload[:4_000], 0o644); err != nil {
		t.Fatal(err)
	}

	for name, sample := range map[string][]byte{
		"empty":            {},
		"longer than data": testPayload(5_000),
	} {
		res := f.resume(t, "c.bin", 10_000, 4_000, sample)
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s sample: status %d, want 400", name, res.StatusCode)
		}
	}
}

func TestTusResumeNeedsModifyPermission(t *testing.T) {
	f := newTusTestFixture(t)
	payload := testPayload(10_000)
	if err := os.WriteFile(filepath.Join(f.scope, "d.bin"), payload[:4_000], 0o644); err != nil {
		t.Fatal(err)
	}

	// Same scope, but a user who may create files and not modify them.
	key := []byte("test-signing-key-2")
	perm := users.Permissions{Create: true}
	st := scopedUserStorage(t, f.scope, perm, key)
	cache := newMemoryUploadCache()
	t.Cleanup(cache.Close)
	srv := newTusTestServer(t, st, cache)
	limited := &tusTestFixture{srv: srv, client: srv.Client(), token: signToken(t, perm, key), scope: f.scope}

	res := limited.resume(t, "d.bin", 10_000, 4_000, payload[:4_000])
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, want 403", res.StatusCode)
	}
}
