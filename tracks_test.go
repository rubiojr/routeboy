package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"

	"code.rbel.co/rubiojr/fade/components/boxedlist"
	"code.rbel.co/rubiojr/fade/components/button"
	"code.rbel.co/rubiojr/fade/components/mapview"
	"code.rbel.co/rubiojr/fade/components/mapview/store"
	"code.rbel.co/rubiojr/fade/components/viewswitcher"
	"code.rbel.co/rubiojr/fade/components/window"
)

// walk writes a GPX file of a track that starts at a time and climbs 10 m
// for every point, heading north from a place near Madrid.
func walk(t *testing.T, path, name string, start time.Time, east float64, points int) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0"?><gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1"><trk><name>%s</name><trkseg>`, name)
	for i := range points {
		fmt.Fprintf(&b, `<trkpt lat="%f" lon="%f"><ele>%d</ele><time>%s</time></trkpt>`,
			40.40+float64(i)*0.001, -3.70+east, 600+10*i, start.Add(time.Duration(i)*time.Minute).Format(time.RFC3339))
	}
	b.WriteString(`</trkseg></trk></gpx>`)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// folder makes a tracks folder: two walks, a file that is not GPX and
// files to leave out.
func folder(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	walk(t, filepath.Join(dir, "penalara.gpx"), "Subida a Peñalara", time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC), 0, 30)
	walk(t, filepath.Join(dir, "2025", "loop.GPX"), "Cercedilla loop", time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC), 0.02, 12)
	walk(t, filepath.Join(dir, ".hidden", "secret.gpx"), "Hidden", time.Now(), 0, 3)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a track"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(dir, "broken.gpx")
	if err := os.WriteFile(broken, []byte("<gpx>"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(broken, old, old); err != nil {
		t.Fatal(err)
	}
	return dir
}

// loaded opens the pages on a tracks folder and reads it.
func loaded(t *testing.T, width float32, dir string, a fyne.App) (*pages, *viewswitcher.View, *window.Window) {
	t.Helper()
	p, view, w := openedWith(t, width, store.NewMemory(1<<20), dir, a)
	q := &queue{}
	p.tracks.do = q.do
	t.Cleanup(func() { p.close(); settle(p.tracks, q) })
	queues[p.tracks] = q
	t.Cleanup(func() { delete(queues, p.tracks) })
	p.tracks.load()
	settle(p.tracks, q)
	return p, view, w
}

// queues holds the queue of every library under test.
var queues = map[*library]*queue{}

// settle waits for the reading of a library and hands over the results,
// until nothing is left to read.
func settle(l *library, q *queue) {
	for {
		l.workers.Wait()
		q.mu.Lock()
		pending := q.pending
		q.pending = nil
		q.mu.Unlock()
		if len(pending) == 0 {
			return
		}
		for _, f := range pending {
			f()
		}
	}
}

// rowsOf returns the titles and subtitles of the track list.
func rowsOf(l *library) (titles, subtitles []string) {
	for _, row := range l.list.Rows() {
		action := row.(*boxedlist.ActionRow)
		titles, subtitles = append(titles, action.Title), append(subtitles, action.Subtitle)
	}
	return titles, subtitles
}

// byName finds a track of the folder.
func byName(t *testing.T, l *library, name string) *track {
	t.Helper()
	for _, tr := range l.all {
		if tr.name == name {
			return tr
		}
	}
	t.Fatalf("no track %q", name)
	return nil
}

func TestTracksAreListedNewestFirstAndSearched(t *testing.T) {
	p, _, _ := loaded(t, 420, folder(t), test.NewApp())
	l := p.tracks
	titles, subtitles := rowsOf(l)
	if got := strings.Join(titles, "|"); got != "Subida a Peñalara|Cercedilla loop|broken" {
		t.Fatalf("rows %q", got)
	}
	if want := "3.2 km · ↑ 290 m · 1 Jun 2025"; subtitles[0] != want {
		t.Errorf("subtitle %q, want %q", subtitles[0], want)
	}
	if !strings.HasPrefix(subtitles[2], "Not a track: ") {
		t.Errorf("a broken file reads %q", subtitles[2])
	}

	for text, want := range map[string]string{
		"penalara":   "Subida a Peñalara",
		"LOOP cerce": "Cercedilla loop",
		"2025":       "",
		"loop.gpx":   "Cercedilla loop",
		"zzz":        "No tracks match",
		"":           "Subida a Peñalara|Cercedilla loop|broken",
	} {
		l.search.SetText(text)
		titles, _ := rowsOf(l)
		if text == "2025" {
			// Names and file names are searched, not dates or folders.
			want = "No tracks match"
		}
		if got := strings.Join(titles, "|"); got != want {
			t.Errorf("search %q: rows %q, want %q", text, got, want)
		}
	}
}

func TestEmptyOrMissingFolderSaysWhatToDo(t *testing.T) {
	dir := t.TempDir()
	for _, tt := range []struct {
		dir, title string
	}{
		{dir, "No tracks yet"},
		{filepath.Join(dir, "missing"), "No tracks folder"},
	} {
		p, _, _ := loaded(t, 420, tt.dir, test.NewApp())
		titles, subtitles := rowsOf(p.tracks)
		if len(titles) != 1 || titles[0] != tt.title || !strings.Contains(subtitles[0], "GPX files") {
			t.Errorf("%s: rows %q, %q", tt.dir, titles, subtitles)
		}
	}
	if got := shortPath("/a/very/long/path/to/the/folder/of/Tracks"); got != "…/Tracks" {
		t.Errorf("long path shortened to %q", got)
	}
	if home, err := os.UserHomeDir(); err == nil {
		if got := shortPath(filepath.Join(home, "Tracks")); got != "~/Tracks" {
			t.Errorf("home path shortened to %q", got)
		}
	}
}

func TestCheckedTracksAreDrawnWithTheCurrentOneInTheBar(t *testing.T) {
	p, _, _ := loaded(t, 420, folder(t), test.NewApp())
	l, d := p.tracks, p.details
	q := queues[l]
	penalara, loop := byName(t, l, "Subida a Peñalara"), byName(t, l, "Cercedilla loop")
	if d.sheet.RevealBottomBar() || p.screen.inset != 0 || len(l.lines()) != 0 {
		t.Fatal("a bar or tracks before any track was checked")
	}

	test.Tap(penalara.row)
	settle(l, q)
	if !penalara.check.Checked || l.current != penalara || p.tracksView.Badge != 1 {
		t.Fatalf("checked %v, current %v, badge %d", penalara.check.Checked, l.current, p.tracksView.Badge)
	}
	if lines := l.lines(); len(lines) != 2 || lines[1].Color != penalara.color || lines[0].Width != currentCasing {
		t.Fatalf("lines %+v, want the track over its casing", lines)
	}
	if !d.sheet.RevealBottomBar() || d.barTitle.Text != "Subida a Peñalara" || d.barSummary.Text != penalara.summary() {
		t.Fatalf("bar shown %v, title %q, summary %q", d.sheet.RevealBottomBar(), d.barTitle.Text, d.barSummary.Text)
	}
	if want := d.bar.MinSize().Height; p.screen.inset != want || p.screen.m.Size().Height != p.screen.Size().Height-want {
		t.Errorf("inset %v, map %v in a screen of %v, want the map above a bar of %v", p.screen.inset, p.screen.m.Size(), p.screen.Size(), want)
	}
	if got := len(d.profile.Samples()); got != 30 || d.note.Visible() {
		t.Errorf("profile of %d samples, note %q", got, d.note.Text)
	}

	penalara.check.OnChanged(true) // checked again: nothing changes
	test.Tap(loop.check)
	settle(l, q)
	if l.current != loop || len(l.lines()) != 3 || loop.color == penalara.color || p.tracksView.Badge != 2 {
		t.Fatalf("current %v, %d lines, colors %v and %v", l.current.name, len(l.lines()), loop.color, penalara.color)
	}
	if d.title.Text != "Cercedilla loop" || d.stats[statDistance].Text != "1.2 km" || d.stats[statTime].Text != "11 min" {
		t.Errorf("sheet title %q, distance %q, time %q", d.title.Text, d.stats[statDistance].Text, d.stats[statTime].Text)
	}

	test.Tap(loop.row)
	if l.current != penalara || loop.check.Checked || len(l.lines()) != 2 {
		t.Fatalf("unchecking the current track left %v current", l.current.name)
	}
	d.hide.OnTapped()
	if l.current != nil || penalara.check.Checked || len(l.lines()) != 0 || p.tracksView.Badge != 0 {
		t.Fatal("Hide Track left a track shown")
	}
	if d.sheet.RevealBottomBar() || p.screen.inset != 0 || p.screen.m.Size() != p.screen.Size() {
		t.Error("the bar stayed after the last track was hidden")
	}
}

func TestBarKeepsTheControlsAboveIt(t *testing.T) {
	p, _, _ := loaded(t, 420, folder(t), test.NewApp())
	s := p.screen
	before := s.m.Locate(madrid.Latitude, madrid.Longitude)
	test.Tap(byName(t, p.tracks, "Cercedilla loop").row)
	settle(p.tracks, queues[p.tracks])
	if after := s.m.Locate(madrid.Latitude, madrid.Longitude); math.Abs(float64(after.Y-before.Y)) > 0.5 {
		t.Errorf("the map moved from %v to %v under the bar", before, after)
	}
	bar := p.details.sheet.BottomBarHeight()
	if bottom := s.controls.Position().Y + s.controls.Size().Height; bottom > s.Size().Height-bar-edge {
		t.Errorf("controls reach %v, under a bar of %v in a screen of %v", bottom, bar, s.Size())
	}
}

func TestShowFitsTheTrackOnTheMap(t *testing.T) {
	p, _, _ := loaded(t, 420, folder(t), test.NewApp())
	penalara := byName(t, p.tracks, "Subida a Peñalara")
	p.stack.Select(tracksPage)
	show := penalara.row.Suffixes()[0].(*button.Button)
	test.Tap(show)
	settle(p.tracks, queues[p.tracks])
	if p.stack.Selected() != mapPage || p.tracks.current != penalara {
		t.Fatalf("page %d, current %v after Show", p.stack.Selected(), p.tracks.current)
	}
	s := p.screen
	size := s.m.Size()
	top := edge + s.search.MinSize().Height
	for _, corner := range [][2]float64{{penalara.stats.South, penalara.stats.West}, {penalara.stats.North, penalara.stats.East}} {
		at := s.m.Locate(corner[0], corner[1])
		if at.X < edge || at.X > size.Width-s.controls.Size().Width-2*edge || at.Y < top || at.Y > size.Height-edge {
			t.Errorf("corner %v at %v, outside the clear part of a map of %v", corner, at, size)
		}
	}
	if zoom := s.m.Center().Zoom; zoom < 13 || zoom > fitZoom {
		t.Errorf("zoom %v for a 3 km track", zoom)
	}
	// A track of one point is shown at the closest zoom.
	s.fit(40, -3, 40, -3)
	if at := s.m.Center(); at.Zoom != fitZoom {
		t.Errorf("zoom %v for a point", at.Zoom)
	}
}

func TestTapOnTheMapChoosesTheTrackUnderIt(t *testing.T) {
	p, _, _ := loaded(t, 420, folder(t), test.NewApp())
	l := p.tracks
	penalara, loop := byName(t, l, "Subida a Peñalara"), byName(t, l, "Cercedilla loop")
	test.Tap(penalara.row)
	test.Tap(loop.row)
	settle(l, queues[l])
	p.screen.m.MoveTo(mapview.Position{Latitude: 40.405, Longitude: -3.69, Zoom: 14})

	test.TapAt(p.screen.m, p.screen.m.Locate(40.4105, -3.7001))
	if l.current != penalara || p.details.track != penalara {
		t.Fatalf("current %v after a tap on Peñalara", l.current.name)
	}
	test.TapAt(p.screen.m, p.screen.m.Locate(40.39, -3.65)) // far from both
	if l.current != penalara {
		t.Fatal("a tap away from the tracks changed the current one")
	}
	if got := p.prefs.String(currentKey); got != penalara.path {
		t.Errorf("saved current %q", got)
	}
}

func TestProfileMarksItsPointOnTheMap(t *testing.T) {
	p, _, _ := loaded(t, 420, folder(t), test.NewApp())
	penalara := byName(t, p.tracks, "Subida a Peñalara")
	test.Tap(penalara.row)
	settle(p.tracks, queues[p.tracks])
	d := p.details
	d.sheet.SetOpen(true)
	profile := d.profile
	profile.MouseMoved(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(profile.Size().Width, 40)}})
	cursor := p.screen.cursor
	if cursor == nil || cursor.Color != penalara.color || math.Abs(cursor.Latitude-40.429) > 1e-9 {
		t.Fatalf("cursor %+v, want the last point in the track's color", cursor)
	}
	markers := 0
	p.screen.m.OnMarkerTapped = func(int) { markers++ }
	test.TapAt(p.screen.m, p.screen.m.Locate(cursor.Latitude, cursor.Longitude))
	if markers != 1 {
		t.Error("the cursor is not on the map")
	}
	d.sheet.OnChanged(false) // what the sheet reports once the user closes it
	if p.screen.cursor != nil || profile.Cursor() != -1 {
		t.Error("closing the sheet left the cursor")
	}
}

func TestShownTracksAreRestored(t *testing.T) {
	dir := folder(t)
	a := test.NewApp()
	p, _, _ := loaded(t, 420, dir, a)
	test.Tap(byName(t, p.tracks, "Cercedilla loop").row)
	test.Tap(byName(t, p.tracks, "Subida a Peñalara").row)
	settle(p.tracks, queues[p.tracks])
	p.tracks.setCurrent(byName(t, p.tracks, "Cercedilla loop"))

	again, _, _ := loaded(t, 420, dir, a)
	l := again.tracks
	var names []string
	for _, tr := range l.shown {
		names = append(names, tr.name)
	}
	if strings.Join(names, "|") != "Cercedilla loop|Subida a Peñalara" || l.current == nil || l.current.name != "Cercedilla loop" {
		t.Fatalf("restored %q, current %v", names, l.current)
	}
	if len(l.lines()) != 3 || !l.shown[0].check.Checked || again.tracksView.Badge != 2 || !again.details.sheet.RevealBottomBar() {
		t.Error("restored tracks are not drawn, checked and counted")
	}
}

func TestReadingAgainKeepsTracksShown(t *testing.T) {
	dir := folder(t)
	p, _, _ := loaded(t, 420, dir, test.NewApp())
	l := p.tracks
	old := byName(t, l, "Cercedilla loop")
	test.Tap(old.row)
	settle(l, queues[l])

	walk(t, filepath.Join(dir, "2025", "loop.GPX"), "Cercedilla loop", time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC), 0.02, 20)
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(dir, "2025", "loop.GPX"), later, later); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "penalara.gpx")); err != nil {
		t.Fatal(err)
	}
	p.stack.OnChanged(tracksPage) // opening Tracks reads the folder again
	settle(l, queues[l])
	loop := byName(t, l, "Cercedilla loop")
	if loop == old || l.current != loop || !slices.Equal(l.shown, []*track{loop}) || loop.color != old.color {
		t.Fatal("the changed track was not shown in place of the old one")
	}
	if titles, _ := rowsOf(l); strings.Join(titles, "|") != "Cercedilla loop|broken" {
		t.Errorf("rows %q after a file was removed", titles)
	}
	if got := len(p.details.profile.Samples()); got != 20 {
		t.Errorf("profile of %d samples, want the new file's", got)
	}
}

func TestShownTracksAreLimited(t *testing.T) {
	dir := t.TempDir()
	for i := range tracksShown + 1 {
		walk(t, filepath.Join(dir, fmt.Sprintf("%02d.gpx", i)), fmt.Sprintf("Walk %02d", i), time.Date(2025, 1, 1+i, 9, 0, 0, 0, time.UTC), float64(i)*0.01, 3)
	}
	p, _, _ := loaded(t, 420, dir, test.NewApp())
	l := p.tracks
	for _, tr := range l.all {
		test.Tap(tr.row)
	}
	settle(l, queues[l])
	last := l.all[len(l.all)-1]
	if len(l.shown) != tracksShown || last.check.Checked || slices.Contains(l.shown, last) {
		t.Fatalf("%d tracks shown, the last checked %v", len(l.shown), last.check.Checked)
	}
	if titles, _ := rowsOf(l); !strings.HasPrefix(titles[0], "At most 20 tracks") {
		t.Errorf("first row %q, want the limit", titles[0])
	}
	if colors := len(trackColors); l.shown[0].color == l.shown[1].color || l.shown[0].color != l.shown[colors].color {
		t.Error("colors do not cycle")
	}
	test.Tap(l.all[0].row)
	if titles, _ := rowsOf(l); strings.HasPrefix(titles[0], "At most") {
		t.Error("the limit stayed after a track was hidden")
	}
}

func TestFormatsOfTheSheet(t *testing.T) {
	for _, tt := range []struct{ got, want string }{
		{duration(0), "—"}, {duration(59 * time.Second), "1 min"}, {duration(3*time.Hour + 5*time.Minute), "3 h 05 min"},
		{distance(850), "850 m"}, {distance(12345), "12.3 km"},
		{short("A very long name for a track of many words", 20), "A very long name fo…"},
	} {
		if tt.got != tt.want {
			t.Errorf("%q, want %q", tt.got, tt.want)
		}
	}
	if fold("Peñalara ÁVILA") != "penalara avila" {
		t.Errorf("folded %q", fold("Peñalara ÁVILA"))
	}
}
