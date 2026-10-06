package cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/omarrsherif/go-cache-api/internal/models"
)

const opTimeout = 100 * time.Millisecond

func newTestCache(t *testing.T) (*Cache, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	c := New(mr.Addr(), opTimeout, 10)
	t.Cleanup(func() { c.Close() })
	return c, mr
}

func TestRoundTrip(t *testing.T) {
	c, mr := newTestCache(t)
	ctx := context.Background()
	key := ProductKey(42)
	in := models.Product{ID: 42, Name: "Widget", Price: "9.99", Quantity: 3}

	var out models.Product
	if found, err := c.Get(ctx, key, &out); err != nil || found {
		t.Fatalf("empty cache: found=%v err=%v", found, err)
	}
	if err := c.Set(ctx, key, in, time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if found, err := c.Get(ctx, key, &out); err != nil || !found || out != in {
		t.Fatalf("Get: found=%v err=%v out=%+v", found, err, out)
	}
	if ttl := mr.TTL(key); ttl != time.Minute {
		t.Fatalf("ttl = %v, want 1m", ttl)
	}

	mr.FastForward(time.Minute + time.Second)
	if found, err := c.Get(ctx, key, &out); err != nil || found {
		t.Fatalf("after expiry: found=%v err=%v", found, err)
	}
}

func TestDelete(t *testing.T) {
	c, mr := newTestCache(t)
	ctx := context.Background()
	key := ProductKey(1)
	if err := c.Set(ctx, key, models.Product{ID: 1}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, key, ProductKey(999)); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if mr.Exists(key) {
		t.Fatal("key still present after Delete")
	}
	if err := c.Delete(ctx); err != nil {
		t.Fatalf("Delete with no keys: %v", err)
	}
}

func TestCorruptValue(t *testing.T) {
	c, mr := newTestCache(t)
	key := ProductKey(7)
	mr.Set(key, "{not json")
	var out models.Product
	if _, err := c.Get(context.Background(), key, &out); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestUnreachableFailsFast(t *testing.T) {
	c := New("127.0.0.1:1", opTimeout, 10) // nothing listens on port 1
	defer c.Close()
	start := time.Now()
	var out models.Product
	_, err := c.Get(context.Background(), "k", &out)
	if err == nil {
		t.Fatal("expected error from unreachable Redis")
	}
	if elapsed := time.Since(start); elapsed > 3*opTimeout {
		t.Fatalf("Get took %v, want < %v", elapsed, 3*opTimeout)
	}
}

func TestServerGoesAway(t *testing.T) {
	c, mr := newTestCache(t)
	ctx := context.Background()
	if err := c.Set(ctx, "k", models.Product{ID: 1}, time.Minute); err != nil {
		t.Fatal(err)
	}
	mr.Close()
	var out models.Product
	if _, err := c.Get(ctx, "k", &out); err == nil {
		t.Fatal("expected error after Redis closed")
	}
	if err := c.Set(ctx, "k", models.Product{ID: 1}, time.Minute); err == nil {
		t.Fatal("expected Set error after Redis closed")
	}
}
