-- Products table. Mounted into MySQL's docker-entrypoint-initdb.d by
-- docker-compose.yml, so it runs once when the data volume is first created.
CREATE TABLE IF NOT EXISTS products (
    id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    name       VARCHAR(255)    NOT NULL,
    price      DECIMAL(10,2)   NOT NULL,
    quantity   INT             NOT NULL DEFAULT 0,
    created_at TIMESTAMP       NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP       NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
