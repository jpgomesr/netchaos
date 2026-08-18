package netchaos

import (
	"sync"
	"time"
)

// deadline is a reusable helper for one direction (read or write) of a
// conn's deadline. It composes with select: channel() returns the current
// "something changed, re-check" signal, and a fresh call to expired() after
// waking is what actually decides whether the deadline has passed — the
// channel firing never means "expired" by itself, exactly like pipe's
// notify channel. That is what makes it safe to bump the channel
// unconditionally on every set() (clearing, re-arming, or an already-past
// deadline all bump it) and what guarantees a concurrent Set*Deadline call
// reliably wakes an in-flight Read or Write.
//
// deadline uses only time.Timer/time.AfterFunc, so testing/synctest
// virtualizes it automatically.
type deadline struct {
	mu    sync.Mutex
	t     time.Time
	timer *time.Timer
	gen   chan struct{}
}

func newDeadline() *deadline {
	return &deadline{gen: make(chan struct{})}
}

// channel returns the current generation channel: it closes on the next
// state change (armed, cleared, or fired).
func (d *deadline) channel() <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.gen
}

// expired reports whether a deadline is set and has passed.
func (d *deadline) expired() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return !d.t.IsZero() && !d.t.After(time.Now())
}

// bumpLocked wakes every current waiter. Must be called with d.mu held.
func (d *deadline) bumpLocked() {
	close(d.gen)
	d.gen = make(chan struct{})
}

// set installs t as the deadline. A zero time.Time clears it. A t that has
// already passed is reflected synchronously (expired() is true as soon as
// set returns) rather than waiting on an async timer callback, so an
// in-flight waiter is guaranteed to wake promptly.
func (d *deadline) set(t time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	d.t = t
	d.bumpLocked()

	if t.IsZero() || !t.After(time.Now()) {
		return
	}
	d.timer = time.AfterFunc(time.Until(t), func() {
		d.mu.Lock()
		d.bumpLocked()
		d.mu.Unlock()
	})
}

// stop cancels any pending timer without changing the recorded deadline
// time, so an already-expired deadline still reports expired() == true.
func (d *deadline) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}
