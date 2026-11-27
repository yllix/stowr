package share

import (
	"testing"
	"time"
)

func TestLinkDownloadLimit(t *testing.T) {
	unlimited := &Link{Downloads: 50}
	if unlimited.Exhausted() {
		t.Error("a link without a limit must never be exhausted")
	}
	if _, limited := unlimited.DownloadsLeft(); limited {
		t.Error("a link without a limit must not report downloads left")
	}

	l := &Link{MaxDownloads: 3, Downloads: 2}
	if left, limited := l.DownloadsLeft(); !limited || left != 1 {
		t.Errorf("DownloadsLeft = %d, %v; want 1, true", left, limited)
	}
	if l.Exhausted() {
		t.Error("a link with one download left must not be exhausted")
	}

	l.Downloads = 3
	if !l.Exhausted() {
		t.Error("a link that used its last download must be exhausted")
	}
	l.Downloads = 7 // e.g. the limit was lowered
	if left, _ := l.DownloadsLeft(); left != 0 {
		t.Errorf("DownloadsLeft = %d past the limit, want 0", left)
	}
}

func TestLinkExpired(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	for name, tc := range map[string]struct {
		expire int64
		want   bool
	}{
		"never":  {0, false},
		"future": {now.Unix() + 1, false},
		"now":    {now.Unix(), true},
		"past":   {now.Unix() - 1, true},
	} {
		if got := (&Link{Expire: tc.expire}).Expired(now); got != tc.want {
			t.Errorf("%s: Expired = %v, want %v", name, got, tc.want)
		}
	}
}

type memBackend struct{ links map[string]*Link }

func (m *memBackend) All() ([]*Link, error) {
	var out []*Link
	for _, l := range m.links {
		c := *l
		out = append(out, &c)
	}
	return out, nil
}

func (m *memBackend) FindByUserID(uint) ([]*Link, error) { return m.All() }

func (m *memBackend) GetPermanent(string, uint) (*Link, error) { return nil, nil }

func (m *memBackend) Gets(string, uint) ([]*Link, error) { return m.All() }

func (m *memBackend) GetByHash(h string) (*Link, error) {
	c := *m.links[h]
	return &c, nil
}

func (m *memBackend) Save(l *Link) error {
	c := *l
	m.links[l.Hash] = &c
	return nil
}

func (m *memBackend) Delete(h string) error {
	delete(m.links, h)
	return nil
}

func (m *memBackend) DeleteWithPathPrefix(string, uint) error { return nil }

// Removing expired links used to delete from the slice while ranging over it,
// which skipped the link after each expired one and repeated the last link.
func TestStorageDropsEveryExpiredLink(t *testing.T) {
	past := time.Now().Add(-time.Hour).Unix()
	back := &memBackend{links: map[string]*Link{
		"a": {Hash: "a", Expire: past},
		"b": {Hash: "b", Expire: past},
		"c": {Hash: "c"},
		"d": {Hash: "d", Expire: past},
		"e": {Hash: "e"},
	}}
	s := NewStorage(back)

	links, err := s.All()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, l := range links {
		got[l.Hash]++
	}
	if len(links) != 2 || got["c"] != 1 || got["e"] != 1 {
		t.Fatalf("All() = %v, want exactly c and e once each", got)
	}
	if len(back.links) != 2 {
		t.Fatalf("expired links left in storage: %d links remain", len(back.links))
	}
}

func TestStorageCountDownloadStopsAtLimit(t *testing.T) {
	back := &memBackend{links: map[string]*Link{"h": {Hash: "h", MaxDownloads: 2}}}
	s := NewStorage(back)

	for i := range 2 {
		ok, err := s.CountDownload("h")
		if err != nil || !ok {
			t.Fatalf("download %d: ok=%v err=%v, want it allowed", i+1, ok, err)
		}
	}
	ok, err := s.CountDownload("h")
	if err != nil || ok {
		t.Fatalf("download past the limit: ok=%v err=%v, want it refused", ok, err)
	}
	if back.links["h"].Downloads != 2 {
		t.Fatalf("Downloads = %d, want 2", back.links["h"].Downloads)
	}
}
