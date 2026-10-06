package main

import (
	"strconv"
	"strings"

	"fyne.io/fyne/v2"

	"code.rbel.co/rubiojr/fade/components/boxedlist"
	"code.rbel.co/rubiojr/fade/components/mapview"
)

// named are the parts of Routeboy an autopilot script can name:
//
//	map        the map
//	search     the search box over the map
//	results    the places it found, and result1, result2… each of them
//	bar        the bar of the current track at the bottom of the map
//	profile    the elevation profile in the track's sheet
//	filter     the search box of the Tracks page
//	track1…    the tracks listed on the Tracks page, newest first
func (p *pages) named(name string) fyne.CanvasObject {
	switch name {
	case "map":
		return p.screen.m
	case "search":
		return p.screen.search
	case "results":
		return p.screen.resultsPanel
	case "bar":
		return p.details.bar
	case "profile":
		return p.details.profile
	case "filter":
		return p.tracks.search
	}
	if n, ok := strings.CutPrefix(name, "result"); ok {
		return nth(p.screen.results.Rows(), n, func(boxedlist.Row) bool { return true })
	}
	if n, ok := strings.CutPrefix(name, "track"); ok {
		rows := map[boxedlist.Row]bool{}
		for _, t := range p.tracks.all {
			if t.row != nil {
				rows[t.row] = true
			}
		}
		return nth(p.tracks.list.Rows(), n, func(r boxedlist.Row) bool { return rows[r] })
	}
	return nil
}

// nth is the row numbered n, from 1, among those that count.
func nth(rows []boxedlist.Row, n string, counts func(boxedlist.Row) bool) fyne.CanvasObject {
	i, err := strconv.Atoi(n)
	if err != nil || i < 1 {
		return nil
	}
	for _, r := range rows {
		if counts(r) {
			if i--; i == 0 {
				return r
			}
		}
	}
	return nil
}

// idle is true once the tracks are read, a search has its results and the
// map has drawn what it can.
func (p *pages) idle() bool {
	if !p.tracks.read || p.tracks.reading || p.screen.cancel != nil {
		return false
	}
	for _, t := range p.tracks.shown {
		if t.reading {
			return false
		}
	}
	switch p.screen.m.Status().State {
	case mapview.Starting, mapview.Loading:
		return false
	}
	return true
}
