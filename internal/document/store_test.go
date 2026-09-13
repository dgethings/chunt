package document_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/dgethings/chunt/internal/document"
)

// TestStore_Concurrent verifies the Store is safe under concurrent
// Get/Put/Delete stress across many URIs. Run with -race.
func TestStore_Concurrent(t *testing.T) {
	t.Parallel()
	s := document.NewStore()

	const workers = 16
	const iters = 200

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				uri := fmt.Sprintf("file:///doc-%d.ios.j2", (seed+i)%50)
				switch i % 3 {
				case 0:
					s.Put(document.New(uri, "cisco_ios_jinja2", i, []byte("x")))
				case 1:
					_, _ = s.Get(uri)
				case 2:
					s.Delete(uri)
				}
			}
		}(w)
	}
	wg.Wait()
}

// TestStore_PutGetDelete covers the basic single-goroutine contract.
func TestStore_PutGetDelete(t *testing.T) {
	t.Parallel()
	s := document.NewStore()

	if _, err := s.Get("file:///missing"); err == nil {
		t.Fatalf("Get on missing URI should return an error")
	}
	doc := document.New("file:///x", "ios", 1, []byte("hello"))
	s.Put(doc)
	got, err := s.Get("file:///x")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.URI != "file:///x" {
		t.Errorf("URI: got %q, want file:///x", got.URI)
	}
	s.Delete("file:///x")
	if _, err := s.Get("file:///x"); err == nil {
		t.Errorf("Get after Delete should return an error")
	}
}
