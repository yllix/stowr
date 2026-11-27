package share

import (
	"sync"
	"time"

	fberrors "github.com/yllix/stowr/v3/errors"
)

// StorageBackend is the interface to implement for a share storage.
type StorageBackend interface {
	All() ([]*Link, error)
	FindByUserID(id uint) ([]*Link, error)
	GetByHash(hash string) (*Link, error)
	GetPermanent(path string, id uint) (*Link, error)
	Gets(path string, id uint) ([]*Link, error)
	Save(s *Link) error
	Delete(hash string) error
	DeleteWithPathPrefix(path string, userID uint) error
}

// Storage is a storage.
type Storage struct {
	back StorageBackend
	// counting serializes download counting, so concurrent downloads can't
	// both read the same count and go past the limit.
	counting sync.Mutex
}

// NewStorage creates a share links storage from a backend.
func NewStorage(back StorageBackend) *Storage {
	return &Storage{back: back}
}

// live deletes the expired links and returns the rest.
func (s *Storage) live(links []*Link) ([]*Link, error) {
	now := time.Now()
	kept := links[:0]
	for _, link := range links {
		if link.Expired(now) {
			if err := s.Delete(link.Hash); err != nil {
				return nil, err
			}
			continue
		}
		kept = append(kept, link)
	}
	return kept, nil
}

// All wraps a StorageBackend.All.
func (s *Storage) All() ([]*Link, error) {
	links, err := s.back.All()
	if err != nil {
		return nil, err
	}
	return s.live(links)
}

// FindByUserID wraps a StorageBackend.FindByUserID.
func (s *Storage) FindByUserID(id uint) ([]*Link, error) {
	links, err := s.back.FindByUserID(id)
	if err != nil {
		return nil, err
	}
	return s.live(links)
}

// GetByHash wraps a StorageBackend.GetByHash.
func (s *Storage) GetByHash(hash string) (*Link, error) {
	link, err := s.back.GetByHash(hash)
	if err != nil {
		return nil, err
	}

	if link.Expired(time.Now()) {
		if err := s.Delete(link.Hash); err != nil {
			return nil, err
		}
		return nil, fberrors.ErrNotExist
	}

	return link, nil
}

// GetPermanent wraps a StorageBackend.GetPermanent
func (s *Storage) GetPermanent(path string, id uint) (*Link, error) {
	return s.back.GetPermanent(path, id)
}

// Gets wraps a StorageBackend.Gets
func (s *Storage) Gets(path string, id uint) ([]*Link, error) {
	links, err := s.back.Gets(path, id)
	if err != nil {
		return nil, err
	}
	return s.live(links)
}

// CountDownload records one download through the link. It returns false,
// recording nothing, when the link has already used up its download limit.
func (s *Storage) CountDownload(hash string) (bool, error) {
	s.counting.Lock()
	defer s.counting.Unlock()

	link, err := s.GetByHash(hash)
	if err != nil {
		return false, err
	}
	if link.Exhausted() {
		return false, nil
	}
	link.Downloads++
	if err := s.back.Save(link); err != nil {
		return false, err
	}
	return true, nil
}

// Save wraps a StorageBackend.Save
func (s *Storage) Save(l *Link) error {
	return s.back.Save(l)
}

// Delete wraps a StorageBackend.Delete
func (s *Storage) Delete(hash string) error {
	return s.back.Delete(hash)
}

func (s *Storage) DeleteWithPathPrefix(path string, userID uint) error {
	return s.back.DeleteWithPathPrefix(path, userID)
}
