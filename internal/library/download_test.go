package library

import (
	"fmt"
	"testing"
)

// Every album has to be downloaded by exactly one worker. The bug this guards
// against handed each worker every nth album and then, because
// --playlist-items also applies to nested playlists, every nth track within
// those, leaving most of an artist's catalogue unfetched for good.
func TestShardAlbumsCoversEveryAlbumOnce(t *testing.T) {
	for _, total := range []int{0, 1, 3, 4, 5, 11, 146, 259} {
		for _, workers := range []int{1, 2, 4, 8} {
			var albums []string
			for i := 0; i < total; i++ {
				albums = append(albums, fmt.Sprintf("album-%d", i))
			}
			seen := map[string]int{}
			for _, w := range shardAlbums(albums, workers) {
				if w.shard != "" {
					t.Fatalf("album sharding must not use item selection, got %q", w.shard)
				}
				for _, u := range w.urls {
					seen[u]++
				}
			}
			if len(seen) != total {
				t.Fatalf("total=%d workers=%d: covered %d albums", total, workers, len(seen))
			}
			for a, n := range seen {
				if n != 1 {
					t.Fatalf("total=%d workers=%d: %s downloaded %d times", total, workers, a, n)
				}
			}
		}
	}
}

// No worker should be handed an empty list of work.
func TestShardAlbumsSkipsIdleWorkers(t *testing.T) {
	got := shardAlbums([]string{"a", "b"}, 8)
	if len(got) != 2 {
		t.Fatalf("want 2 workers for 2 albums, got %d", len(got))
	}
}
