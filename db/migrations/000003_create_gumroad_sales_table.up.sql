CREATE TABLE gumroad_sales (
    id VARCHAR(255) PRIMARY KEY,
    product_id VARCHAR(255) NOT NULL,
    product_name TEXT,
    permalink VARCHAR(255),
    product_permalink TEXT,
    email VARCHAR(255) NOT NULL,
    price BIGINT,
    gumroad_fee BIGINT,
    currency VARCHAR(10),
    quantity INTEGER,
    discover_fee_charged BOOLEAN,
    can_contact BOOLEAN,
    referrer TEXT,
    order_number BIGINT,
    sale_id VARCHAR(255),
    sale_timestamp TIMESTAMPTZ,
    purchaser_id VARCHAR(255),
    subscription_id VARCHAR(255),
    license_key VARCHAR(255),
    is_multiseat_license BOOLEAN,
    ip_country VARCHAR(255),
    recurrence VARCHAR(50),
    is_gift_receiver_purchase BOOLEAN,
    refunded BOOLEAN,
    disputed BOOLEAN,
    dispute_won BOOLEAN,
    created_at TIMESTAMPTZ NOT NULL,
    chargebacked BOOLEAN,
    subscription_ended_at TIMESTAMPTZ,
    subscription_cancelled_at TIMESTAMPTZ,
    subscription_failed_at TIMESTAMPTZ,
    -- Raw JSON data from the API for any fields not explicitly mapped or for future use
    raw_data JSONB,
    -- IncentivizeThis-specific fields
    it_notified BOOLEAN DEFAULT FALSE,
    it_api_key TEXT
);

CREATE INDEX IF NOT EXISTS idx_gumroad_sales_product_id ON gumroad_sales (product_id);
CREATE INDEX IF NOT EXISTS idx_gumroad_sales_email ON gumroad_sales (email);
CREATE INDEX IF NOT EXISTS idx_gumroad_sales_created_at ON gumroad_sales (created_at);
CREATE INDEX IF NOT EXISTS idx_gumroad_sales_sale_timestamp ON gumroad_sales (sale_timestamp);
CREATE INDEX IF NOT EXISTS idx_gumroad_sales_subscription_id ON gumroad_sales (subscription_id);
CREATE INDEX IF NOT EXISTS idx_gumroad_sales_license_key ON gumroad_sales (license_key);