// Package models holds the data types shared across layers.
package models

import "time"

// Product is one row of the products table. Price is kept as a string so the
// DECIMAL(10,2) value round-trips exactly, without float rounding.
type Product struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Price     string    `json:"price"`
	Quantity  int       `json:"quantity"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
