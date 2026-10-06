// Command routeboy shows GPX tracks on a map: a folder of tracks to search
// and choose from, drawn on a map that can be panned, zoomed and searched,
// with the details and elevation profile of a track in a bottom sheet.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"code.rbel.co/rubiojr/fade/autopilot"
	"code.rbel.co/rubiojr/fade/components/mapview"
	"code.rbel.co/rubiojr/fade/components/mapview/oms"
	"code.rbel.co/rubiojr/fade/components/mapview/store"
	"code.rbel.co/rubiojr/fade/components/window"
	"code.rbel.co/rubiojr/fade/device"
	"code.rbel.co/rubiojr/fade/style"
)

// settings are what the command line chooses.
type settings struct {
	server     string
	tracks     string
	home       mapview.Position
	scheme     style.Scheme
	megabytes  int64
	offline    bool
	fullscreen bool
}

func main() {
	s, err := parse(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "routeboy:", err)
		os.Exit(2)
	}
	// A soft limit makes the collector work harder before the device runs
	// short of memory.
	debug.SetMemoryLimit(mapview.MemoryLimit())

	// All Fyne calls run on the UI goroutine, so Fyne can skip checking
	// which goroutine each refresh comes from.
	app.SetMetadata(fyne.AppMetadata{ID: "co.rbel.routeboy", Name: "Routeboy", Migrations: map[string]bool{"fyneDo": true}})
	a := app.NewWithID("co.rbel.routeboy")
	a.Settings().SetTheme(style.NewTheme(style.WithScheme(s.scheme)))
	data, server, err := connect(a, s)
	if err != nil {
		fmt.Fprintln(os.Stderr, "routeboy:", err)
		os.Exit(2)
	}

	w := window.New(a, "Routeboy")
	screen := newScreen(w.Header.SetSubtitle, server, mapview.New(server, mapview.Options{Start: s.home}), s.home)
	pages := newPages(screen, data, s, a.Preferences())
	pages.mount(w)
	w.Autopilot = autopilot.Options{Named: pages.named, Idle: pages.idle}
	// Read the tracks once the application runs: until then there is no
	// Fyne goroutine for the result to go to.
	a.Lifecycle().SetOnStarted(pages.tracks.load)
	w.SetOnClosed(pages.close)
	w.Resize(windowSize())
	w.SetFullScreen(s.fullscreen)
	w.ShowAndRun()
	screen.workers.Wait()
	pages.tracks.workers.Wait()
}

// connect opens the store of map data and a client of the server that keeps
// what it downloads there.
func connect(a fyne.App, s settings) (store.Store, *oms.Client, error) {
	data := mapview.Keep(a, s.megabytes<<20)
	server, err := oms.New(s.server, oms.Options{Store: data, Offline: s.offline, UserAgent: "routeboy/0"})
	return data, server, err
}

// output receives the usage text.
var output io.Writer = os.Stderr

// parse reads the command line.
func parse(arguments []string) (settings, error) {
	var s settings
	flags := flag.NewFlagSet("routeboy", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&s.server, "oms", "https://oms.rbel.co", "OMS server URL")
	flags.StringVar(&s.tracks, "tracks", defaultTracks(), "folder of GPX tracks")
	flags.Float64Var(&s.home.Latitude, "lat", 40.4168, "starting latitude")
	flags.Float64Var(&s.home.Longitude, "lon", -3.7038, "starting longitude")
	flags.Float64Var(&s.home.Zoom, "zoom", 12, "starting zoom, 1 to 18")
	scheme := flags.String("scheme", "system", "color scheme: system, light or dark")
	flags.Int64Var(&s.megabytes, "store-mib", 256, "most map data to keep, in MiB")
	flags.BoolVar(&s.offline, "offline", false, "use kept map data only")
	flags.BoolVar(&s.fullscreen, "fullscreen", device.Mobile(), "open full screen, as an application on a phone does")
	if err := flags.Parse(arguments); err != nil {
		return settings{}, err
	}
	switch *scheme {
	case "system":
		s.scheme = style.SchemeSystem
	case "light":
		s.scheme = style.SchemeLight
	case "dark":
		s.scheme = style.SchemeDark
	default:
		return settings{}, fmt.Errorf("unknown color scheme %q", *scheme)
	}
	if s.megabytes < 1 {
		return settings{}, fmt.Errorf("store of %d MiB", s.megabytes)
	}
	if s.tracks == "" {
		return settings{}, errors.New("no tracks folder")
	}
	// Checked tracks are saved by path. Keep them valid from any directory.
	tracks, err := filepath.Abs(s.tracks)
	if err != nil {
		return settings{}, err
	}
	s.tracks = tracks
	return s, nil
}

// windowSize is the size the window opens at: a phone's on a phone or
// tablet, where it opens full screen anyway, and room for the map on a
// computer.
func windowSize() fyne.Size {
	if device.Mobile() {
		return fyne.NewSize(420, 800)
	}
	return fyne.NewSize(1024, 720)
}

// defaultTracks is the Tracks folder in the user's home.
func defaultTracks() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "Tracks"
	}
	return filepath.Join(home, "Tracks")
}
