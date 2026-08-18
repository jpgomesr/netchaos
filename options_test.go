package netchaos

import "testing"

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
