package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"code.rbel.co/rubiojr/fade/components/mapview/oms"
)

// planet serves an index and tiles of 600 KiB, and counts the tiles asked for.
func planet(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var tiles atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/maps/planet" {
			_, _ = w.Write([]byte(`{"tiles":["https://elsewhere.example/maps/planet/snapshot_1/{z}/{x}/{y}.pbf"],"maxzoom":14}`))
			return
		}
		if !strings.HasSuffix(r.URL.Path, ".pbf") {
			http.NotFound(w, r)
			return
		}
		tiles.Add(1)
		_, _ = w.Write(bytes.Repeat([]byte{1}, 600<<10))
	}))
	t.Cleanup(server.Close)
	return server, &tiles
}

// tile opens the map data as a start of the application does and asks for
// one tile.
func tile(t *testing.T, s settings, x uint32) error {
	t.Helper()
	a := test.NewApp()
	defer a.Quit()
	_, server, err := connect(a, s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Open(context.Background()); err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = server.Tile(context.Background(), 12, x, 2)
	return err
}

func TestMapDataIsDownloadedOnceAndKeptWithinTheBudget(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)
	server, tiles := planet(t)
	s := settings{server: server.URL, megabytes: 1}

	for start := range 2 { // the second start finds the tile of the first
		if err := tile(t, s, 1); err != nil {
			t.Fatalf("start %d: %v", start+1, err)
		}
	}
	if tiles.Load() != 1 {
		t.Fatalf("%d downloads of one tile over two starts, want 1", tiles.Load())
	}

	offline := s
	offline.offline = true
	if err := tile(t, offline, 1); err != nil {
		t.Fatalf("offline, a kept tile: %v", err)
	}
	if err := tile(t, offline, 9); !errors.Is(err, oms.ErrOffline) {
		t.Fatalf("offline, a tile never seen: got %v, want %v", err, oms.ErrOffline)
	}
	if tiles.Load() != 1 {
		t.Fatalf("%d downloads: offline must never touch the network", tiles.Load())
	}

	// Two tiles of 600 KiB do not fit in 1 MiB, so the older one goes.
	if err := tile(t, s, 2); err != nil {
		t.Fatal(err)
	}
	if err := tile(t, s, 1); err != nil {
		t.Fatal(err)
	}
	if tiles.Load() != 3 {
		t.Fatalf("%d downloads, want 3: the store kept more than its budget", tiles.Load())
	}
}

func TestTheWindowIsPhoneSizedOnlyOnAPhone(t *testing.T) {
	t.Setenv("FADE_DEVICE", "mobile")
	if got := windowSize(); got != fyne.NewSize(420, 800) {
		t.Errorf("on a phone: %v", got)
	}
	t.Setenv("FADE_DEVICE", "desktop")
	if got := windowSize(); got.Width < 1000 || got.Height > 720 {
		t.Errorf("on a computer: %v", got)
	}
}
