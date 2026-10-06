package repo

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/joho/godotenv"

	"github.com/omarrsherif/go-cache-api/internal/config"
	"github.com/omarrsherif/go-cache-api/internal/models"
)

// testDB connects to the real MySQL from .env. These are integration tests;
// they skip (not fail) when the database is unreachable.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	_ = godotenv.Load("../../.env")

	cfg, err := config.Load()
	if err != nil {
		t.Skipf("skipping: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	db, err := New(ctx, cfg)
	if err != nil {
		t.Skipf("skipping: cannot reach MySQL: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestProductCRUD(t *testing.T) {
	ctx := context.Background()
	r := NewProductRepo(testDB(t))

	id, err := r.Create(ctx, models.Product{Name: "Test Widget", Price: "19.99", Quantity: 5})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = r.Delete(ctx, id) })

	got, err := r.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "Test Widget" || got.Price != "19.99" || got.Quantity != 5 {
		t.Fatalf("GetByID returned %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Error("created_at was not populated")
	}

	got.Name = "Updated Widget"
	got.Quantity = 12
	if err := r.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	after, err := r.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if after.Name != "Updated Widget" || after.Quantity != 12 {
		t.Fatalf("update did not persist: %+v", after)
	}

	if err := r.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := r.GetByID(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: want ErrNotFound, got %v", err)
	}
}

func TestMissingProduct(t *testing.T) {
	ctx := context.Background()
	r := NewProductRepo(testDB(t))
	const missing = int64(-1)

	if _, err := r.GetByID(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID: want ErrNotFound, got %v", err)
	}
	if err := r.Update(ctx, models.Product{ID: missing, Name: "x", Price: "1.00"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update: want ErrNotFound, got %v", err)
	}
	if err := r.Delete(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete: want ErrNotFound, got %v", err)
	}
}
