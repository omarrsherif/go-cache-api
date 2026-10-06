package metrics

import "testing"

func TestSnapshotAndSub(t *testing.T) {
	m := New()
	if m.Snapshot().HitRate != 0 {
		t.Fatal("hit rate with no lookups should be 0")
	}
	m.CacheHits.Add(3)
	m.CacheMisses.Add(1)
	m.DBReads.Add(1)
	before := m.Snapshot()
	if before.HitRate != 0.75 {
		t.Fatalf("hit rate = %v, want 0.75", before.HitRate)
	}

	m.CacheHits.Add(1)
	m.CacheMisses.Add(1)
	d := m.Snapshot().Sub(before)
	if d.CacheHits != 1 || d.CacheMisses != 1 || d.DBReads != 0 || d.HitRate != 0.5 {
		t.Fatalf("unexpected delta: %+v", d)
	}
}
