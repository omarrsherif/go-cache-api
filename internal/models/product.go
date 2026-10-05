// Package models holds the plain Go "shapes" of the things in our database.
// There is no database code in here at all: these are just containers that
// describe what a product looks like.
package models

// "time" is Go's built-in package for dates and times.
// We need it because two of our columns are timestamps.
import "time"

// Product describes ONE row of the products table.
//
// Each field lines up with one column in schema.sql. When we read a row out
// of MySQL we copy each column into the matching field here, and when we
// save a product we send these fields back to MySQL.
//
// The text in backticks after each field is called a "tag". The `json:"..."`
// part tells Go what to call this field when it is turned into JSON for a
// web response. Go field names must start with a capital letter to be
// usable outside this package, but JSON is normally lowercase, so the tag
// gives us "id" in the JSON output instead of "ID".
type Product struct {
	// The unique number MySQL gave this product. int64 is a whole number
	// big enough to match the BIGINT column in the database.
	ID int64 `json:"id"`

	// The product name, e.g. "Blue T-Shirt".
	Name string `json:"name"`

	// The price, kept as text (a string) on purpose.
	//
	// It looks strange to store a price as "19.99" instead of a number, but
	// Go's decimal number type (float64) cannot hold 19.99 exactly - it
	// actually stores something like 19.989999999999998. Keeping the price
	// as the exact text MySQL gave us means no cents ever go missing.
	Price string `json:"price"`

	// How many are in stock. int is a normal whole number.
	Quantity int `json:"quantity"`

	// When this product was first added. time.Time is Go's date-and-time type.
	// We never set this ourselves - MySQL fills it in.
	CreatedAt time.Time `json:"created_at"`

	// When this product was last edited. MySQL also updates this one for us.
	UpdatedAt time.Time `json:"updated_at"`
}
