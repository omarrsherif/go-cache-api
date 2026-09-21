package repo

import (
	"context"
	"database/sql"
	"errors"

	// "testing" is Go's built-in test toolkit. Any file ending in _test.go is
	// not part of the real program - it is only compiled when running tests.
	"testing"

	"github.com/joho/godotenv"

	"github.com/omarrsherif/GO-PROJECT/internal/config"
	"github.com/omarrsherif/GO-PROJECT/internal/models"
)

// testDB opens a database connection for a test to use.
//
// These are "integration" tests: they talk to the real MySQL running in
// Docker rather than a pretend one. That means they genuinely prove our SQL
// is correct, but it also means they cannot run if MySQL is switched off -
// so when the database is unreachable we SKIP instead of FAIL. Skipping keeps
// "go test ./..." green on a machine that has not started Docker, while still
// being honest that the checks did not actually run.
func testDB(t *testing.T) *sql.DB {
	// t.Helper marks this as a helper function. If something goes wrong, Go
	// then points at the line in the actual test that called us, which is far
	// more useful than pointing in here.
	t.Helper()

	// Tests run from inside the internal/repo folder, so the .env file two
	// folders up would not be found automatically. We point at it directly.
	_ = godotenv.Load("../../.env")

	cfg, err := config.Load()
	if err != nil {
		// t.Skipf stops this test and marks it "skipped", not "failed".
		t.Skipf("skipping: %v", err)
	}

	db, err := New(cfg)
	if err != nil {
		t.Skipf("skipping: cannot reach MySQL: %v", err)
	}

	// t.Cleanup registers work to do once the test finishes, whether it
	// passed or failed. Closing the connection here means we never leak it.
	t.Cleanup(func() { db.Close() })

	return db
}

// TestProductCRUD walks one product through its whole life: create it, read
// it back, change it, read it again, delete it, and confirm it is gone.
//
// Doing it in that order matters, because each step checks the previous one
// really happened in the database rather than just in memory.
func TestProductCRUD(t *testing.T) {
	// context.Background is the plain, empty starting context - the right one
	// to use when there is no request or deadline to inherit from.
	ctx := context.Background()
	r := NewProductRepo(testDB(t))

	// --- CREATE -------------------------------------------------------
	id, err := r.Create(ctx, models.Product{Name: "Test Widget", Price: "19.99", Quantity: 5})
	if err != nil {
		// t.Fatalf reports the problem and stops this test immediately.
		// We use Fatal (not Error) when carrying on makes no sense - there
		// is no point reading back a product that was never created.
		t.Fatalf("Create: %v", err)
	}

	// Make sure this test row is removed from the real database even if a
	// step below fails, so repeated runs do not pile up junk rows.
	t.Cleanup(func() { _ = r.Delete(ctx, id) })

	// --- READ ---------------------------------------------------------
	got, err := r.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	// Check the values that came back out of MySQL match what we put in.
	if got.Name != "Test Widget" || got.Price != "19.99" || got.Quantity != 5 {
		// %+v prints the whole struct with field names, so a failure message
		// shows exactly what we got instead.
		t.Fatalf("GetByID returned %+v", got)
	}

	// created_at is filled in by MySQL, not by us. If it is still the zero
	// value ("year 1"), then the automatic timestamp is not working.
	if got.CreatedAt.IsZero() {
		// t.Error reports a failure but lets the test carry on, because the
		// remaining steps are still worth checking.
		t.Error("created_at was not populated")
	}

	// --- UPDATE -------------------------------------------------------
	got.Name = "Updated Widget"
	got.Quantity = 12
	if err := r.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Read it a second time. This is the important part: it proves the change
	// was really written to MySQL, not just changed in our local copy.
	after, err := r.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if after.Name != "Updated Widget" || after.Quantity != 12 {
		t.Fatalf("update did not persist: %+v", after)
	}

	// --- DELETE -------------------------------------------------------
	if err := r.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Looking it up now should give our ErrNotFound. The "!" means "not", so
	// this reads: if the error is NOT ErrNotFound, something is wrong.
	if _, err := r.GetByID(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: want ErrNotFound, got %v", err)
	}
}

// TestMissingProduct checks the "this does not exist" path of all three
// functions that can hit it. Handling missing records properly is easy to get
// wrong and easy to forget, so it gets a test of its own.
func TestMissingProduct(t *testing.T) {
	ctx := context.Background()
	r := NewProductRepo(testDB(t))

	// AUTO_INCREMENT only ever produces positive numbers, so -1 is an id that
	// is guaranteed never to exist. That is safer than guessing a number like
	// 999999, which could actually be a real product one day.
	const missing = int64(-1)

	// Each of the three should tell us the product is not there, rather than
	// silently doing nothing or crashing.
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
