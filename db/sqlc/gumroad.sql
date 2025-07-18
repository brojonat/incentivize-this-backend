-- name: InsertGumroadSale :exec
INSERT INTO gumroad_sales (
    id,
    product_id,
    product_name,
    permalink,
    product_permalink,
    email,
    price,
    gumroad_fee,
    currency,
    quantity,
    discover_fee_charged,
    can_contact,
    referrer,
    order_number,
    sale_id,
    sale_timestamp,
    purchaser_id,
    subscription_id,
    license_key,
    is_multiseat_license,
    ip_country,
    recurrence,
    is_gift_receiver_purchase,
    refunded,
    disputed,
    dispute_won,
    created_at,
    chargebacked,
    subscription_ended_at,
    subscription_cancelled_at,
    subscription_failed_at,
    it_notified,
    it_api_key
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17, $18, $19, $20,
    $21, $22, $23, $24, $25, $26, $27, $28, $29, $30,
    $31, $32, $33
)
ON CONFLICT (id) DO NOTHING;

-- name: GetUnnotifiedGumroadSales :many
SELECT *
FROM gumroad_sales
WHERE it_notified IS DISTINCT FROM TRUE;

-- name: UpdateGumroadSaleNotification :exec
UPDATE gumroad_sales
SET it_notified = TRUE, it_api_key = @api_key
WHERE id = @id;

-- name: GetExistingGumroadSaleIDs :many
SELECT id FROM gumroad_sales WHERE id = ANY(@sale_ids::text[]);