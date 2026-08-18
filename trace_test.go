package netchaos

import "testing"

func TestTraceRecordsInOrder(t *testing.T) {
	var r traceRecorder
	r.record(faultEvent{dropped: true})
	r.record(faultEvent{effective: 5})
	r.record(faultEvent{partitioned: true})

	got := r.snapshot()
	if len(got) != 3 {
		t.Fatalf("len(snapshot) = %d, want 3", len(got))
	}
	for i, want := range []uint64{0, 1, 2} {
		if got[i].seq != want {
			t.Fatalf("event %d: seq = %d, want %d", i, got[i].seq, want)
		}
	}
	if !got[0].dropped || got[1].effective != 5 || !got[2].partitioned {
		t.Fatalf("recorded events lost their field values: %+v", got)
	}
}

func TestTraceComparable(t *testing.T) {
	build := func() []faultEvent {
		var r traceRecorder
		r.record(faultEvent{dropped: true, drawn: 10})
		r.record(faultEvent{effective: 5})
		return r.snapshot()
	}

	a, b := build(), build()
	if len(a) != len(b) {
		t.Fatalf("snapshots have different lengths: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("event %d differs: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestTraceSnapshotIsACopy(t *testing.T) {
	var r traceRecorder
	r.record(faultEvent{dropped: true})

	snap := r.snapshot()
	snap[0].dropped = false

	if got := r.snapshot(); !got[0].dropped {
		t.Fatalf("mutating a snapshot affected the recorder's own state")
	}
}
