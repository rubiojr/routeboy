package main

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"time"

	"fyne.io/fyne/v2/driver"

	"code.rbel.co/rubiojr/fade/components/button"
	"code.rbel.co/rubiojr/fade/components/mapview"
	"code.rbel.co/rubiojr/fade/icons"
	"code.rbel.co/rubiojr/gogps"
)

// farthest is the least accurate fix shown, in metres: GeoIP guesses are
// tens of kilometres off.
const farthest = 5000

// complaint is how long a location problem stays on the map.
const complaint = 8 * time.Second

// locator shows where the device is, from its location service, and keeps
// the map on it. Its button starts it and follows, with a spinner until the
// first fix; a tap while following or still waiting stops it, one while the
// map has been dragged away follows again. It runs on the Fyne goroutine.
type locator struct {
	s       *screen
	button  *button.Button
	cancel  context.CancelFunc
	session *gogps.Session
	// attempt numbers the starts, so what a stopped one reports late is
	// recognised and dropped.
	attempt uint64
	// waiting is true from a start until the first fix.
	waiting bool
}

func newLocator(s *screen) *locator {
	l := &locator{s: s}
	l.button = button.NewIcon(icons.Raw("find-location"), l.tapped)
	s.m.OnFollowing = l.button.SetChecked
	return l
}

func (l *locator) tapped() {
	switch {
	case l.waiting:
		l.stop()
	case l.session == nil:
		l.start()
	case l.s.m.Following():
		l.stop()
	default:
		l.s.m.SetFollowing(true)
	}
}

// start asks the location service for fixes. On Android that asks the user
// the first time, so it waits off the Fyne goroutine.
func (l *locator) start() {
	l.attempt++
	attempt := l.attempt
	l.waiting = true
	l.button.SetBusy(true)
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	vm, activity := androidActivity()
	l.s.workers.Go(func() {
		session, err := gogps.Start(ctx,
			gogps.WithDesktopID("co.rbel.routeboy"),
			gogps.WithAndroidActivity(vm, activity),
			gogps.WithMaxRadius(farthest))
		l.s.do(func() { l.started(attempt, session, err) })
		if err != nil {
			return
		}
		for fix := range session.Fixes() {
			l.s.do(func() { l.fixed(attempt, fix) })
		}
		l.s.do(func() { l.ended(attempt, session.Err()) })
	})
}

func (l *locator) started(attempt uint64, session *gogps.Session, err error) {
	if attempt != l.attempt || l.cancel == nil {
		return // stopped meanwhile
	}
	if err != nil {
		l.stop()
		l.complain(err)
		return
	}
	l.session = session
	l.s.notice.Tell("", "", 0)
	// The map follows from the first fix.
	l.s.m.SetFollowing(true)
}

func (l *locator) fixed(attempt uint64, fix gogps.Fix) {
	if attempt != l.attempt || l.session == nil {
		return
	}
	if l.waiting {
		l.waiting = false
		l.button.SetBusy(false)
	}
	l.s.m.SetHere(mapview.Here{Latitude: fix.Latitude, Longitude: fix.Longitude, Accuracy: fix.Accuracy})
}

// ended hears of a session that stopped by itself.
func (l *locator) ended(attempt uint64, err error) {
	if attempt != l.attempt || l.session == nil {
		return // stopped here already
	}
	l.stop()
	if err != nil {
		l.complain(err)
	}
}

// stop ends the session, which releases the service as it winds down, or
// the wait for one.
func (l *locator) stop() {
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
	l.session = nil
	l.waiting = false
	l.button.SetBusy(false)
	l.s.m.ClearHere()
}

// complain tells the user what kept the location from being read.
func (l *locator) complain(err error) {
	message, detail := describeLocationError(err)
	l.s.notice.Tell(message, detail, complaint)
}

// describeLocationError puts a location error in the user's terms.
func describeLocationError(err error) (message, detail string) {
	switch {
	case errors.Is(err, gogps.ErrDisabled):
		return "Location is switched off", "Turn it on in the system settings to see where you are."
	case errors.Is(err, gogps.ErrDenied):
		return "Location access was refused", "Allow Routeboy to use your location in the system settings."
	case errors.Is(err, gogps.ErrUnavailable) && runtime.GOOS != "android":
		return "No location service", "Routeboy reads your location from GeoClue on Linux."
	case errors.Is(err, gogps.ErrUnavailable):
		return "No location service", beyond(err, gogps.ErrUnavailable)
	}
	return "Your location could not be read", err.Error()
}

// beyond is what an error says beyond its kind, as a sentence.
func beyond(err, kind error) string {
	words := strings.TrimPrefix(err.Error(), kind.Error()+": ")
	if words == "" || words == err.Error() {
		return words
	}
	return strings.ToUpper(words[:1]) + words[1:] + "."
}

// androidActivity is the Java VM and the activity on Android, for gogps,
// and zeros elsewhere.
func androidActivity() (vm, activity uintptr) {
	driver.RunNative(func(c any) error {
		if ac, ok := c.(*driver.AndroidContext); ok {
			vm, activity = ac.VM, ac.Ctx
		}
		return nil
	})
	return vm, activity
}
