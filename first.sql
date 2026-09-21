-- This file creates the table that stores our products.
-- Run it once against the MySQL database before starting the Go app.

-- "CREATE TABLE IF NOT EXISTS" means: make this table, but if a table
-- named "products" is already there, do nothing instead of failing.
-- That way running this file twice is safe.
CREATE TABLE IF NOT EXISTS products (

    -- The unique number for each product: 1, 2, 3, and so on.
    -- BIGINT UNSIGNED = a whole number, very large, never negative.
    -- NOT NULL        = this box can never be left empty.
    -- AUTO_INCREMENT  = MySQL fills this in for us and counts up by itself,
    --                   so we never pick an id when adding a product.
    id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,

    -- The product's name, e.g. "Blue T-Shirt".
    -- VARCHAR(255) = text, up to 255 characters long.
    name       VARCHAR(255)    NOT NULL,

    -- The price, e.g. 19.99.
    -- DECIMAL(10,2) = a number with 10 digits in total, 2 of them after
    -- the dot. We use DECIMAL and not a "float" because floats store money
    -- slightly wrong (19.99 might become 19.989999...) and those tiny
    -- errors add up. DECIMAL stores the exact value we typed.
    price      DECIMAL(10,2)   NOT NULL,

    -- How many we have in stock.
    -- DEFAULT 0 = if we add a product without saying the quantity,
    -- MySQL puts 0 in automatically.
    quantity   INT             NOT NULL DEFAULT 0,

    -- The date and time the row was added.
    -- DEFAULT CURRENT_TIMESTAMP = MySQL stamps "right now" for us on insert.
    created_at TIMESTAMP       NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- The date and time the row was last changed.
    -- ON UPDATE CURRENT_TIMESTAMP = every time we edit this row, MySQL
    -- automatically refreshes this to the current time. We never set it.
    updated_at TIMESTAMP       NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    -- The primary key is the column that identifies a row.
    -- It must be unique, and MySQL builds an index on it so that
    -- looking a product up by its id is fast.
    PRIMARY KEY (id)

-- ENGINE=InnoDB  = the part of MySQL that does the actual storing. InnoDB is
--                  the normal choice; it supports transactions and can enforce
--                  links between tables.
-- CHARSET=utf8mb4 = which characters we can store. utf8mb4 covers every
--                   language plus emoji. (Plain "utf8" in MySQL is an older,
--                   incomplete version that cannot store emoji.)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
