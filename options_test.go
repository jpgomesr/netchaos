package netchaos

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestNewNetworkDefaults(t *testing.T) {
	n := NewNetwork()
	if n == nil {
		t.Fatal("NewNetwork() = nil, want a usable *Network")
	}
	if n.seed != defaultSeed {
		t.Fatalf("seed = %d, want the documented default %d", n.seed, defaultSeed)
	}
}

func TestOptionOrderPrecedence(t *testing.T) {
	n := NewNetwork(WithSeed(1), WithSeed(2))
	if n.seed != 2 {
		t.Fatalf("seed = %d, want 2 (later option should override earlier)", n.seed)
	}
}

func TestWithSeedStored(t *testing.T) {
	n := NewNetwork(WithSeed(42))
	if n.seed != 42 {
		t.Fatalf("seed = %d, want 42", n.seed)
	}
}

// TestDefaultSeedRecoverable exercises the decision recorded in NewNetwork's
// godoc: the default seed is a fixed, documented constant (not random), so
// "recovering" it from a failing run is just reading defaultSeed — there is
// no exported Network.Seed() accessor in M1 because a fixed default has
// nothing to recover at runtime.
func TestDefaultSeedRecoverable(t *testing.T) {
	n := NewNetwork()
	if n.seed != defaultSeed {
		t.Fatalf("seed = %d, want the documented constant defaultSeed = %d", n.seed, defaultSeed)
	}
}

// expectPanic runs fn, requiring it to panic with a string value containing
// every one of wantSubstrings -- the mechanism M0-5 fixed (NewNetwork
// panics on invalid options) plus the requirement that every message name
// the offending option and value.
func expectPanic(t *testing.T, wantSubstrings []string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected a panic, got none")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value is not a string: %#v", r)
		}
		for _, s := range wantSubstrings {
			if !strings.Contains(msg, s) {
				t.Fatalf("panic message %q does not contain %q", msg, s)
			}
		}
	}()
	fn()
}

func TestOptionValidation(t *testing.T) {
	cases := []struct {
		name string
		opt  Option
		want []string // substrings the panic message must contain
	}{
		{"loss rate negative", WithPacketLoss(-0.1), []string{"WithPacketLoss", "-0.1"}},
		{"loss rate above one", WithPacketLoss(1.1), []string{"WithPacketLoss", "1.1"}},
		{"loss rate NaN", WithPacketLoss(math.NaN()), []string{"WithPacketLoss"}},
		{"loss rate +Inf", WithPacketLoss(math.Inf(1)), []string{"WithPacketLoss"}},
		{"loss rate -Inf", WithPacketLoss(math.Inf(-1)), []string{"WithPacketLoss"}},
		{"latency min greater than max", WithLatency(2*time.Second, time.Second), []string{"WithLatency"}},
		{"latency negative min", WithLatency(-time.Millisecond, time.Millisecond), []string{"WithLatency"}},
		{"latency negative max", WithLatency(0, -time.Millisecond), []string{"WithLatency"}},
		{"partition empty peerA", WithPartition("", "b"), []string{"WithPartition"}},
		{"partition empty peerB", WithPartition("a", ""), []string{"WithPartition"}},
		{"partition identical peers", WithPartition("x", "x"), []string{"WithPartition", "x"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expectPanic(t, c.want, func() {
				NewNetwork(c.opt)
			})
		})
	}
}

// TestOptionValidationUsesFinalValue asserts validation runs once, after
// every Option has been applied -- an invalid intermediate value overridden
// by a later, valid option of the same kind must not panic.
func TestOptionValidationUsesFinalValue(t *testing.T) {
	n := NewNetwork(WithPacketLoss(-1), WithPacketLoss(0.5))
	if n.faults.lossRate != 0.5 {
		t.Fatalf("lossRate = %v, want 0.5 (the later, valid option should win and not panic on the earlier invalid intermediate value)", n.faults.lossRate)
	}
}

func TestOptionBoundaryValuesAccepted(t *testing.T) {
	cases := []struct {
		name string
		opt  Option
	}{
		{"loss rate exactly 0.0", WithPacketLoss(0.0)},
		{"loss rate exactly 1.0", WithPacketLoss(1.0)},
		{"latency min == max", WithLatency(50*time.Millisecond, 50*time.Millisecond)},
		{"zero latency", WithLatency(0, 0)},
		{"valid partition", WithPartition("a", "b")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("NewNetwork panicked on a valid boundary value: %v", r)
				}
			}()
			NewNetwork(c.opt)
		})
	}
}

func TestValidationMessagesNameTheOption(t *testing.T) {
	expectPanic(t, []string{"WithPacketLoss", "1.5"}, func() {
		NewNetwork(WithPacketLoss(1.5))
	})
}
