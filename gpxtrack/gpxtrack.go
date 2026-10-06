// Package gpxtrack reads GPX files with gpxgo and measures their tracks and
// routes for the map: length, climb, time, bounds and the elevation profile.
package gpxtrack

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/tkrajina/gpxgo/gpx"
)

const (
	// MaxBytes is the largest file Read accepts.
	MaxBytes = 16 << 20
	// climbStep is how far the elevation has to move from the last turning
	// point before it counts as ascent or descent, so that the noise of a
	// recording doesn't add up to a climb.
	climbStep = 5.0
	// earthRadius is the mean radius of the Earth, in meters.
	earthRadius = 6371008.8
)

// Track holds the tracks and routes of a GPX file. Each segment is a
// separate line.
type Track struct {
	Name string
	// Segments are the segments of the tracks, then the routes.
	Segments [][]gpx.GPXPoint
	Stats
	// File is the document as gpxgo read it, with its waypoints, metadata
	// and extensions.
	File *gpx.GPX
}

// Stats measure a track. Unlike gpxgo's own, they take in routes too.
type Stats struct {
	// Distance is the length of the segments in meters. The gaps between
	// segments don't count.
	Distance float64
	// Ascent and Descent are the meters climbed and descended.
	Ascent, Descent float64
	// Lowest and Highest are elevations in meters, when HasElevation is set.
	Lowest, Highest float64
	HasElevation    bool
	// Start and End are the first and last times recorded, or zero.
	Start, End time.Time
	Points     int
	// South, West, North and East bound the track, in degrees.
	South, West, North, East float64
}

// Duration is the time from the first point recorded to the last.
func (s Stats) Duration() time.Duration {
	if s.Start.IsZero() || s.End.IsZero() {
		return 0
	}
	return s.End.Sub(s.Start)
}

// Read reads the tracks and routes of a GPX document, named after its
// metadata or its first track or route. It reads at most MaxBytes and does
// not close r.
func Read(r io.Reader) (Track, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return Track{}, err
	}
	if len(data) > MaxBytes {
		return Track{}, errors.New("larger than 16 MiB")
	}
	file, err := gpx.ParseBytes(data)
	if err != nil {
		return Track{}, fmt.Errorf("not GPX: %w", err)
	}
	t := Track{Name: strings.TrimSpace(file.Name), File: file}
	add := func(name string, points []gpx.GPXPoint) {
		if len(points) == 0 {
			return
		}
		if t.Name == "" {
			t.Name = strings.TrimSpace(name)
		}
		t.Segments = append(t.Segments, points)
	}
	for _, track := range file.Tracks {
		for _, segment := range track.Segments {
			add(track.Name, segment.Points)
		}
	}
	for _, route := range file.Routes {
		add(route.Name, route.Points)
	}
	if len(t.Segments) == 0 {
		return Track{}, errors.New("no track or route points")
	}
	if err := valid(t.Segments); err != nil {
		return Track{}, err
	}
	t.Stats = measure(t.Segments)
	return t, nil
}

// valid checks that every point is on the globe.
func valid(segments [][]gpx.GPXPoint) error {
	for _, segment := range segments {
		for _, p := range segment {
			if math.IsNaN(p.Latitude) || math.IsNaN(p.Longitude) || math.Abs(p.Latitude) > 90 || math.Abs(p.Longitude) > 180 {
				return fmt.Errorf("invalid coordinates %v, %v", p.Latitude, p.Longitude)
			}
		}
	}
	return nil
}

// step is the distance between two points, in meters, as gpxgo measures it.
func step(a, b gpx.GPXPoint) float64 {
	return gpx.Distance2D(a.Latitude, a.Longitude, b.Latitude, b.Longitude, false)
}

// elevation is the elevation of a point, if it has a usable one.
func elevation(p gpx.GPXPoint) (float64, bool) {
	if p.Elevation.Null() {
		return 0, false
	}
	e := p.Elevation.Value()
	return e, !math.IsNaN(e) && !math.IsInf(e, 0)
}

func measure(segments [][]gpx.GPXPoint) Stats {
	s := Stats{South: 90, West: 180, North: -90, East: -180, Lowest: math.Inf(1), Highest: math.Inf(-1)}
	for _, segment := range segments {
		c := climb{turn: math.NaN()}
		for i, p := range segment {
			s.Points++
			s.South, s.North = min(s.South, p.Latitude), max(s.North, p.Latitude)
			s.West, s.East = min(s.West, p.Longitude), max(s.East, p.Longitude)
			if i > 0 {
				s.Distance += step(segment[i-1], p)
			}
			if !p.Timestamp.IsZero() {
				if s.Start.IsZero() || p.Timestamp.Before(s.Start) {
					s.Start = p.Timestamp
				}
				if p.Timestamp.After(s.End) {
					s.End = p.Timestamp
				}
			}
			e, ok := elevation(p)
			if !ok {
				continue
			}
			s.HasElevation = true
			s.Lowest, s.Highest = min(s.Lowest, e), max(s.Highest, e)
			c.add(e, &s)
		}
	}
	if !s.HasElevation {
		s.Lowest, s.Highest = 0, 0
	}
	return s
}

// climb counts ascent and descent along a segment. turn is the elevation
// where the slope last changed direction, or the highest or lowest since;
// heading is 1 up, -1 down and 0 before the first step.
type climb struct {
	turn    float64
	heading int
}

func (c *climb) add(elevation float64, s *Stats) {
	switch rise := elevation - c.turn; {
	case math.IsNaN(c.turn):
		c.turn = elevation
	case rise >= climbStep || c.heading > 0 && rise > 0:
		s.Ascent += rise
		c.turn, c.heading = elevation, 1
	case rise <= -climbStep || c.heading < 0 && rise < 0:
		s.Descent -= rise
		c.turn, c.heading = elevation, -1
	}
}

// Sample is a point of the elevation profile, at a distance along the track.
type Sample struct {
	Distance, Elevation, Latitude, Longitude float64
}

// Profile lists the points with an elevation, with their distance along the
// track. The gaps between segments add no distance.
func (t Track) Profile() []Sample {
	var samples []Sample
	distance := 0.0
	for _, segment := range t.Segments {
		for i, p := range segment {
			if i > 0 {
				distance += step(segment[i-1], p)
			}
			if e, ok := elevation(p); ok {
				samples = append(samples, Sample{Distance: distance, Elevation: e, Latitude: p.Latitude, Longitude: p.Longitude})
			}
		}
	}
	return samples
}

// Simplify returns the segments with at most most points in all. It leaves
// out the points that change the shape least, as the Ramer–Douglas–Peucker
// algorithm ranks them, and always keeps the ends of each segment.
func (t Track) Simplify(most int) [][]gpx.Point {
	var weights [][]float64
	var all []float64
	for _, segment := range t.Segments {
		w := importance(segment)
		weights = append(weights, w)
		all = append(all, w...)
	}
	threshold, room := math.Inf(-1), 0
	if len(all) > most {
		slices.SortFunc(all, func(a, b float64) int { return cmp.Compare(b, a) })
		threshold = all[max(0, most-1)]
		// Points that tie with the threshold fill what room is left.
		room = most
		for _, w := range all {
			if w > threshold {
				room--
			}
		}
	}
	result := make([][]gpx.Point, len(t.Segments))
	for i, segment := range t.Segments {
		for j, p := range segment {
			keep := weights[i][j] > threshold
			if !keep && weights[i][j] == threshold && room > 0 {
				keep = true
				room--
			}
			if keep {
				result[i] = append(result[i], p.Point)
			}
		}
	}
	return result
}

// importance is how far each point lies from the line that would replace it,
// in meters, as Ramer–Douglas–Peucker splits the segment. A point never
// matters more than the one whose split found it. The ends are infinite.
func importance(segment []gpx.GPXPoint) []float64 {
	n := len(segment)
	weights := make([]float64, n)
	if n == 0 {
		return weights
	}
	weights[0], weights[n-1] = math.Inf(1), math.Inf(1)
	// A local equirectangular projection is close enough to compare shapes.
	scale := math.Cos(segment[0].Latitude*math.Pi/180) * earthRadius * math.Pi / 180
	x := make([]float64, n)
	y := make([]float64, n)
	for i, p := range segment {
		x[i], y[i] = p.Longitude*scale, p.Latitude*earthRadius*math.Pi/180
	}
	type span struct {
		first, last int
		limit       float64
	}
	stack := []span{{0, n - 1, math.Inf(1)}}
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if s.last-s.first < 2 {
			continue
		}
		dx, dy := x[s.last]-x[s.first], y[s.last]-y[s.first]
		length := math.Hypot(dx, dy)
		far, farthest := s.first+1, -1.0
		for i := s.first + 1; i < s.last; i++ {
			var d float64
			if length == 0 {
				d = math.Hypot(x[i]-x[s.first], y[i]-y[s.first])
			} else {
				d = math.Abs(dy*(x[i]-x[s.first])-dx*(y[i]-y[s.first])) / length
			}
			if d > farthest {
				far, farthest = i, d
			}
		}
		weights[far] = min(farthest, s.limit)
		stack = append(stack, span{s.first, far, weights[far]}, span{far, s.last, weights[far]})
	}
	return weights
}

// Thin returns about 2×stretches samples of a profile at most: the lowest and
// highest of each of that many equal stretches of distance, in order, and
// always the first and the last sample.
func Thin(samples []Sample, stretches int) []Sample {
	n := len(samples)
	if n <= 2*stretches+2 || stretches < 1 {
		return slices.Clone(samples)
	}
	start, span := samples[0].Distance, samples[n-1].Distance-samples[0].Distance
	if span <= 0 {
		return []Sample{samples[0], samples[n-1]}
	}
	result := []Sample{samples[0]}
	stretch, low, high := -1, -1, -1
	flush := func() {
		if low < 0 {
			return
		}
		result = append(result, samples[min(low, high)])
		if low != high {
			result = append(result, samples[max(low, high)])
		}
	}
	for i := 1; i < n-1; i++ {
		s := min(int((samples[i].Distance-start)/span*float64(stretches)), stretches-1)
		if s != stretch {
			flush()
			stretch, low, high = s, i, i
			continue
		}
		if samples[i].Elevation < samples[low].Elevation {
			low = i
		}
		if samples[i].Elevation > samples[high].Elevation {
			high = i
		}
	}
	flush()
	return append(result, samples[n-1])
}
