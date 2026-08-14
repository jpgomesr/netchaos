package netchaos

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestPlaceholderReady(t *testing.T) {
	if !placeholderReady() {
		t.Fatal("placeholderReady() = false, want true")
	}
}

// TestSynctestAvailable proves testing/synctest resolves and virtualizes
// time on this toolchain: the goroutine sleeps for an hour of virtual time,
// the main goroutine blocks receiving from a channel it closes, and the
// whole test still completes in real time on the order of milliseconds.
// M3 depends on this property.
func TestSynctestAvailable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		done := make(chan struct{})
		go func() {
			time.Sleep(time.Hour)
			close(done)
		}()

		<-done

		if elapsed := time.Since(start); elapsed != time.Hour {
			t.Fatalf("virtual time elapsed = %v, want exactly %v", elapsed, time.Hour)
		}
	})
}
