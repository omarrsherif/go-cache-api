package repo

import (
	// "context" carries a cancel signal and a deadline down into the database
	// call. If the person who made the web request gives up and closes their
	// browser, the context is cancelled and MySQL can stop working on a query
	// whose answer nobody is waiting for any more.
	"context"

	"database/sql"

	// "errors" lets us create and compare error values.
	"errors"

	"fmt"

	"github.com/omarrsherif/GO-PROJECT/internal/models"
)

// ErrNotFound is our own error meaning "no product has that id".
//
// We define it once, here, as a single shared value. That lets code elsewhere
// ask "is this specific problem the one that happened?" using
// errors.Is(err, ErrNotFound), and react to a missing product differently
// from, say, the database being unreachable. A web handler would turn this
// one into a 404 page and anything else into a 500.
var ErrNotFound = errors.New("product not found")

// ProductRepo is the object that performs product operations.
//
// It holds the database pool inside it so that the four functions below can
// use it without it being passed in every single time. The lowercase "db"
// means the field is private: only code in this package can touch it.
type ProductRepo struct {
	db *sql.DB
}

// NewProductRepo builds a ProductRepo around a database pool.
//
// The "*" means it returns a pointer - the address of the object rather than
// a copy of it - so everyone shares the same repo and the same pool.
func NewProductRepo(db *sql.DB) *ProductRepo {
	return &ProductRepo{db: db}
}

// ---------------------------------------------------------------------------
// A note about the "?" marks in every query below.
//
// We never glue values into a query with string joining. We write "?" where
// the value goes and pass the real value separately. The database then treats
// it strictly as data, never as a command.
//
// If we built the query by joining text, someone could type a product name
// like:  x'; DROP TABLE products; --
// and the database would run it as an instruction and delete our table. That
// attack is called SQL injection, and the "?" is what prevents it.
// ---------------------------------------------------------------------------

// Create adds a new product and returns the id MySQL assigned to it.
func (r *ProductRepo) Create(ctx context.Context, p models.Product) (int64, error) {
	// We list only three columns. id fills itself in (AUTO_INCREMENT), and
	// created_at / updated_at are stamped by MySQL, so we leave all three out.
	const q = `INSERT INTO products (name, price, quantity) VALUES (?, ?, ?)`

	// ExecContext is for queries that CHANGE data and return no rows back:
	// INSERT, UPDATE, DELETE. The values after the query fill the "?" marks,
	// left to right.
	res, err := r.db.ExecContext(ctx, q, p.Name, p.Price, p.Quantity)
	if err != nil {
		return 0, fmt.Errorf("create product: %w", err)
	}

	// The insert worked, but we do not yet know which id the new row got.
	// LastInsertId asks MySQL what number it just handed out.
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("create product: %w", err)
	}

	// Return the new id and nil, where nil means "no error, all good".
	return id, nil
}

// GetByID looks up a single product.
// If nothing has that id, it returns ErrNotFound.
func (r *ProductRepo) GetByID(ctx context.Context, id int64) (models.Product, error) {
	// We name every column we want instead of writing SELECT *. That way,
	// if someone adds a column to the table later, this code keeps working
	// and the columns stay in the exact order we read them in below.
	const q = `SELECT id, name, price, quantity, created_at, updated_at
	           FROM products WHERE id = ?`

	// An empty Product we are about to fill in.
	var p models.Product

	// QueryRowContext is for when we expect exactly ONE row back.
	//
	// Scan copies the columns into our struct's fields. The "&" gives Scan
	// the *address* of each field, which is what lets it write into them
	// rather than into throwaway copies. The order here must match the order
	// of the columns in the SELECT above.
	err := r.db.QueryRowContext(ctx, q, id).Scan(
		&p.ID, &p.Name, &p.Price, &p.Quantity, &p.CreatedAt, &p.UpdatedAt,
	)

	// sql.ErrNoRows is the specific error meaning "the query ran fine, there
	// was simply no matching row". That is not really a failure, so we swap
	// it for our own clearer ErrNotFound before passing it up.
	if errors.Is(err, sql.ErrNoRows) {
		return models.Product{}, ErrNotFound
	}

	// Any OTHER error is a real problem - database down, bad SQL, and so on.
	if err != nil {
		return models.Product{}, fmt.Errorf("get product %d: %w", id, err)
	}

	return p, nil
}

// Update overwrites an existing product's name, price and quantity.
// If no product has that id, it returns ErrNotFound.
func (r *ProductRepo) Update(ctx context.Context, p models.Product) error {
	// Note the WHERE clause. Without it, this would overwrite EVERY row in
	// the table. The "?" at the end is filled with p.ID.
	// We do not touch updated_at - MySQL refreshes it by itself.
	const q = `UPDATE products SET name = ?, price = ?, quantity = ? WHERE id = ?`

	res, err := r.db.ExecContext(ctx, q, p.Name, p.Price, p.Quantity, p.ID)
	if err != nil {
		return fmt.Errorf("update product %d: %w", p.ID, err)
	}

	// An UPDATE that matches nothing is NOT an error as far as MySQL is
	// concerned - it simply did nothing and reports success. So to find out
	// whether the product existed, we have to ask how many rows it touched.
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update product %d: %w", p.ID, err)
	}

	// Zero rows touched means there was no product with that id.
	// (This is reliable thanks to the clientFoundRows setting in the
	// connection string - see mysql.go.)
	if n == 0 {
		return ErrNotFound
	}

	// nil on its own means "finished successfully, nothing went wrong".
	return nil
}

// Delete removes a product.
// If no product has that id, it returns ErrNotFound.
func (r *ProductRepo) Delete(ctx context.Context, id int64) error {
	// Again, the WHERE clause is what keeps this from emptying the table.
	const q = `DELETE FROM products WHERE id = ?`

	res, err := r.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("delete product %d: %w", id, err)
	}

	// Same idea as Update: deleting something that was never there is not an
	// error to MySQL, so we check the row count to find out what happened.
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete product %d: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}

	return nil
}
