package gpxtrack

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/tkrajina/gpxgo/gpx"
)

const walk = `<?xml version="1.0" encoding="UTF-8"?>
<gpx version="1.1" creator="test" xmlns="http://www.topografix.com/GPX/1/1">
  <metadata><name>Morning walk</name></metadata>
  <wpt lat="40.0" lon="-3.0"><name>Ignored</name></wpt>
  <trk>
    <name>Track name</name>
    <trkseg>
      <trkpt lat="40.0" lon="-3.0"><ele>600</ele><time>2025-03-14T09:00:00Z</time></trkpt>
      <trkpt lat="40.001" lon="-3.0"><ele>610</ele><time>2025-03-14T09:01:00Z</time></trkpt>
      <trkpt lat="40.002" lon="-3.0"><ele>620</ele><time>2025-03-14T09:02:00Z</time></trkpt>
    </trkseg>
    <trkseg>
      <trkpt lat="40.010" lon="-3.0"><ele>620</ele><time>2025-03-14T09:30:00Z</time></trkpt>
      <trkpt lat="40.011" lon="-3.0"><ele>590</ele><time>2025-03-14T09:31:00Z</time></trkpt>
    </trkseg>
  </trk>
</gpx>`

func TestReadMeasuresTheTrack(t *testing.T) {
	track, err := Read(strings.NewReader(walk))
	if err != nil {
		t.Fatal(err)
	}
	if track.Name != "Morning walk" || len(track.Segments) != 2 || track.Points != 5 {
		t.Fatalf("name %q, %d segments, %d points", track.Name, len(track.Segments), track.Points)
	}
	// Three steps of 0.001° of latitude, as gpxgo measures them; the gap
	// between the segments doesn't count.
	if want := 3 * 0.001 * 1000 * 10000.8 / 90; math.Abs(track.Distance-want) > 0.01 {
		t.Errorf("distance %.1f m, want %.1f", track.Distance, want)
	}
	if track.Ascent != 20 || track.Descent != 30 || track.Lowest != 590 || track.Highest != 620 || !track.HasElevation {
		t.Errorf("ascent %v, descent %v, lowest %v, highest %v", track.Ascent, track.Descent, track.Lowest, track.Highest)
	}
	if want := 31 * time.Minute; track.Duration() != want {
		t.Errorf("duration %v, want %v", track.Duration(), want)
	}
	if track.South != 40 || track.North != 40.011 || track.West != -3 || track.East != -3 {
		t.Errorf("bounds %v %v %v %v", track.South, track.West, track.North, track.East)
	}
	profile := track.Profile()
	if len(profile) != 5 || profile[3].Distance != profile[2].Distance || profile[4].Elevation != 590 {
		t.Errorf("profile %+v", profile)
	}
}

func TestReadNamesAndRoutes(t *testing.T) {
	tests := []struct {
		name, file, want string
		segments         int
	}{
		{"track name", `<gpx><trk><name> Ridge </name><trkseg><trkpt lat="1" lon="2"/></trkseg></trk></gpx>`, "Ridge", 1},
		{"GPX 1.0 name", `<gpx version="1.0"><name>Old</name><rte><name>Route</name><rtept lat="1" lon="2"/><rtept lat="1" lon="3"/></rte></gpx>`, "Old", 1},
		{"route", `<gpx><rte><name>Route</name><rtept lat="1" lon="2"/></rte><rte><rtept lat="1" lon="3"/></rte></gpx>`, "Route", 2},
		{"unnamed", `<gpx><trk><trkseg><trkpt lat="1" lon="2"/></trkseg></trk></gpx>`, "", 1},
	}
	for _, tt := range tests {
		track, err := Read(strings.NewReader(tt.file))
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if track.Name != tt.want || len(track.Segments) != tt.segments {
			t.Errorf("%s: name %q, %d segments", tt.name, track.Name, len(track.Segments))
		}
		if track.HasElevation || track.Duration() != 0 || track.Profile() != nil {
			t.Errorf("%s: elevation or times without any in the file", tt.name)
		}
	}
}

func TestReadRejectsWhatIsNotATrack(t *testing.T) {
	for _, file := range []string{
		"",
		"not xml",
		`<gpx><wpt lat="1" lon="2"/></gpx>`,
		`<gpx><trk><trkseg><trkpt lat="91" lon="2"/></trkseg></trk></gpx>`,
		`<gpx><trk><trkseg><trkpt lat="NaN" lon="2"/></trkseg></trk></gpx>`,
		strings.Repeat(" ", MaxBytes+1),
	} {
		if _, err := Read(strings.NewReader(file)); err == nil {
			t.Errorf("%.40q was read", file)
		}
	}
}

func TestClimbIgnoresNoise(t *testing.T) {
	var segment []gpx.GPXPoint
	// Up 100 m in 1 m steps that wobble by 2 m, then down 50 m.
	for i := range 101 {
		e := float64(i)
		if i%2 == 1 {
			e += 2
		}
		segment = append(segment, at(e))
	}
	for i := range 51 {
		segment = append(segment, at(100-float64(i)))
	}
	s := measure([][]gpx.GPXPoint{segment})
	if s.Ascent < 100 || s.Ascent > 102 || s.Descent < 50 || s.Descent > 52 {
		t.Errorf("ascent %v, descent %v, want about 100 and 50", s.Ascent, s.Descent)
	}
	flat := measure([][]gpx.GPXPoint{{at(10), at(13), at(10)}})
	if flat.Ascent != 0 || flat.Descent != 0 {
		t.Errorf("noise counted: ascent %v, descent %v", flat.Ascent, flat.Descent)
	}
}

func TestSimplifyKeepsTheShape(t *testing.T) {
	// A straight line of 1000 points with one corner in the middle.
	var a, b []gpx.GPXPoint
	for i := range 1000 {
		lat := 40 + float64(i)*1e-4
		lon := -3.0
		if i >= 500 {
			lon += float64(i-500) * 1e-4
		}
		a = append(a, gpx.GPXPoint{Point: gpx.Point{Latitude: lat, Longitude: lon}})
	}
	b = append(b, gpx.GPXPoint{Point: gpx.Point{Latitude: 41, Longitude: -3}}, gpx.GPXPoint{Point: gpx.Point{Latitude: 41.1, Longitude: -3}})
	track := Track{Segments: [][]gpx.GPXPoint{a, b}}

	thin := track.Simplify(10)
	if len(thin) != 2 || len(thin[1]) != 2 {
		t.Fatalf("segments %d, last of %d points", len(thin), len(thin[len(thin)-1]))
	}
	total := len(thin[0]) + len(thin[1])
	if total > 10 {
		t.Fatalf("%d points, want at most 10", total)
	}
	first, last := thin[0][0], thin[0][len(thin[0])-1]
	if first != a[0].Point || last != a[999].Point {
		t.Error("the ends of a segment were left out")
	}
	corner := false
	for _, p := range thin[0] {
		corner = corner || p == a[500].Point
	}
	if !corner {
		t.Error("the corner was left out")
	}
	if all := track.Simplify(5000); len(all[0]) != 1000 || len(all[1]) != 2 {
		t.Error("a track within the limit was not kept whole")
	}
}

func TestThinKeepsPeaks(t *testing.T) {
	var samples []Sample
	for i := range 10001 {
		e := 100.0
		if i == 5003 {
			e = 900 // a single spike
		}
		samples = append(samples, Sample{Distance: float64(i), Elevation: e})
	}
	thin := Thin(samples, 100)
	if len(thin) > 202 || thin[0] != samples[0] || thin[len(thin)-1] != samples[10000] {
		t.Fatalf("%d samples from %v to %v", len(thin), thin[0].Distance, thin[len(thin)-1].Distance)
	}
	peak := false
	for i, s := range thin {
		peak = peak || s.Elevation == 900
		if i > 0 && s.Distance <= thin[i-1].Distance {
			t.Fatalf("sample %d at %v after %v", i, s.Distance, thin[i-1].Distance)
		}
	}
	if !peak {
		t.Error("the spike was left out")
	}
	if few := Thin(samples[:50], 100); len(few) != 50 {
		t.Errorf("%d of 50 samples kept", len(few))
	}
}

// at is a point with an elevation.
func at(elevation float64) gpx.GPXPoint {
	var p gpx.GPXPoint
	p.Elevation.SetValue(elevation)
	return p
}

func TestReadWhatGPXWritersVary(t *testing.T) {
	// ISO-8859-1 for "Peñalara", and times without a zone.
	latin := "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?>" +
		"<gpx version=\"1.1\"><metadata><name>Pe\xf1alara</name></metadata><wpt lat=\"40.8\" lon=\"-3.9\"><name>Summit</name></wpt>" +
		"<trk><trkseg><trkpt lat=\"40.8\" lon=\"-3.95\"><time>2025-06-01T09:00:00</time></trkpt>" +
		"<trkpt lat=\"40.81\" lon=\"-3.95\"><time>2025-06-01T10:30:00</time></trkpt></trkseg></trk></gpx>"
	track, err := Read(strings.NewReader(latin))
	if err != nil {
		t.Fatal(err)
	}
	if track.Name != "Peñalara" || track.Duration() != 90*time.Minute {
		t.Errorf("name %q, duration %v", track.Name, track.Duration())
	}
	if track.File == nil || len(track.File.Waypoints) != 1 || track.File.Waypoints[0].Name != "Summit" {
		t.Error("the waypoints of the file are not at hand")
	}
}
