package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/omarrsherif/go-cache-api/internal/models"
)

// ErrNotFound is returned when no product has the requested id.
var ErrNotFound = errors.New("product not found")

// ProductRepo performs product CRUD against MySQL. All queries are
// parameterised.
type ProductRepo struct {
	db *sql.DB
}

// NewProductRepo wraps a connection pool.
func NewProductRepo(db *sql.DB) *ProductRepo {
	return &ProductRepo{db: db}
}

// Create inserts a product and returns the generated id.
func (r *ProductRepo) Create(ctx context.Context, p models.Product) (int64, error) {
	const q = `INSERT INTO products (name, price, quantity) VALUES (?, ?, ?)`
	res, err := r.db.ExecContext(ctx, q, p.Name, p.Price, p.Quantity)
	if err != nil {
		return 0, fmt.Errorf("create product: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("create product: %w", err)
	}
	return id, nil
}

// GetByID returns one product, or ErrNotFound.
func (r *ProductRepo) GetByID(ctx context.Context, id int64) (models.Product, error) {
	const q = `SELECT id, name, price, quantity, created_at, updated_at FROM products WHERE id = ?`
	var p models.Product
	err := r.db.QueryRowContext(ctx, q, id).Scan(
		&p.ID, &p.Name, &p.Price, &p.Quantity, &p.CreatedAt, &p.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Product{}, ErrNotFound
	}
	if err != nil {
		return models.Product{}, fmt.Errorf("get product %d: %w", id, err)
	}
	return p, nil
}

// Update overwrites name, price and quantity. Returns ErrNotFound if the id
// does not exist.
func (r *ProductRepo) Update(ctx context.Context, p models.Product) error {
	const q = `UPDATE products SET name = ?, price = ?, quantity = ? WHERE id = ?`
	res, err := r.db.ExecContext(ctx, q, p.Name, p.Price, p.Quantity, p.ID)
	if err != nil {
		return fmt.Errorf("update product %d: %w", p.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update product %d: %w", p.ID, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a product. Returns ErrNotFound if the id does not exist.
func (r *ProductRepo) Delete(ctx context.Context, id int64) error {
	const q = `DELETE FROM products WHERE id = ?`
	res, err := r.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("delete product %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete product %d: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
