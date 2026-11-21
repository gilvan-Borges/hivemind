package notify

import "sync"

// tokenBook maps a platform id (D-Bus) to the notice id we show the IDE.
type tokenBook struct {
	mu  sync.Mutex
	ids map[uint32]string
}

func newTokenBook() *tokenBook {
	return &tokenBook{ids: map[uint32]string{}}
}

func (b *tokenBook) Put(sys uint32, token string) {
	if b == nil || token == "" {
		return
	}
	b.mu.Lock()
	b.ids[sys] = token
	b.mu.Unlock()
}

func (b *tokenBook) Take(sys uint32) (string, bool) {
	if b == nil {
		return "", false
	}
	b.mu.Lock()
	token, ok := b.ids[sys]
	if ok {
		delete(b.ids, sys)
	}
	b.mu.Unlock()
	return token, ok
}
