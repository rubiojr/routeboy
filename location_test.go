package main

import (
	"errors"
	"fmt"
	"testing"

	"code.rbel.co/rubiojr/gogps"
)

func TestLocationErrorsAreSaidInTheUsersTerms(t *testing.T) {
	cases := []struct {
		err     error
		message string
	}{
		{fmt.Errorf("%w: geoclue Start: no", gogps.ErrDisabled), "Location is switched off"},
		{fmt.Errorf("%w: the user refused", gogps.ErrDenied), "Location access was refused"},
		{fmt.Errorf("%w: no location provider", gogps.ErrUnavailable), "No location service"},
		{errors.New("something else"), "Your location could not be read"},
	}
	for _, c := range cases {
		message, detail := describeLocationError(c.err)
		if message != c.message || detail == "" {
			t.Errorf("%v: %q / %q, want %q with a detail", c.err, message, detail, c.message)
		}
	}
}

func TestBeyondIsTheErrorsOwnWords(t *testing.T) {
	err := fmt.Errorf("%w: no location provider", gogps.ErrUnavailable)
	if got := beyond(err, gogps.ErrUnavailable); got != "No location provider." {
		t.Errorf("beyond %q", got)
	}
	if got := beyond(gogps.ErrUnavailable, gogps.ErrUnavailable); got != gogps.ErrUnavailable.Error() {
		t.Errorf("a bare sentinel gives %q", got)
	}
}
