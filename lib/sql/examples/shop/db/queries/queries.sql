-- The shop's queries. `nomi tool sqlkit generate` writes queries.nomi from
-- them, a function for each.
-- import: shop.CustomerRevenue

-- query: user_by_email(email: String) -> Maybe<User>
-- The user with this email, if any.
SELECT id, email, name, created_at
FROM users
WHERE email = @email;

-- query: insert_user(email: String, name: Maybe<String>) -> Int
-- Add a user, returning its id.
INSERT INTO users (email, name) VALUES (@email, @name)
RETURNING id;

-- query: insert_order(user_id: Int, total: Decimal) -> Int
-- Place an order for a user.
INSERT INTO orders (user_id, total) VALUES (@user_id, @total)
RETURNING id;

-- query: set_status(id: Int, status: OrderStatus) -> Changed
-- Change an order's status: the number of orders changed.
UPDATE orders SET status = @status WHERE id = @id;

-- query: order(id: Int) -> Order
-- One order.
SELECT id, user_id, status, total, placed_at, shipped_on
FROM orders
WHERE id = @id;

-- query: orders_for_user(user_id: Int, limit: Int)
-- A user's orders, newest first, each with the user's email.
SELECT o.id, o.status, o.total, o.placed_at, u.email
FROM orders o
JOIN users u ON u.id = o.user_id
WHERE o.user_id = @user_id
ORDER BY o.placed_at DESC, o.id DESC
LIMIT @limit;

-- query: order_counts()
-- Every user with the number of orders they placed, including those who
-- placed none.
SELECT u.email, max(o.id) AS "last_order?: Int", count(o.id) AS "orders!: Int"
FROM users u
LEFT JOIN orders o ON o.user_id = u.id
GROUP BY u.id
ORDER BY u.email;

-- query: revenue_since(since: Instant) -> List<CustomerRevenue>
-- Revenue per customer from paid and shipped orders since a moment. SQLite
-- declares no type for a sum; the `CAST` declares it DECIMAL(12, 2), so it
-- reads back at scale 2.
SELECT u.id AS user_id, u.email,
       count(*) AS "order_count!",
       CAST(sum(o.total) AS DECIMAL(12, 2)) AS "revenue!"
FROM users u
JOIN orders o ON o.user_id = u.id
WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= @since
GROUP BY u.id, u.email
ORDER BY revenue DESC;

-- query: all_orders() -> Rows<Order>
-- Every order, streamed.
SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id;

-- query: order_status(id: Int) -> OrderStatus
-- One order's status: a query of one column reads it as the enum.
SELECT status FROM orders WHERE id = @id;

-- query: status_if_placed(id: Int) -> Maybe<OrderStatus>
-- An order's status, if there is such an order.
SELECT status FROM orders WHERE id = @id;

-- query: statuses_for_user(user_id: Int) -> List<OrderStatus>
-- The status of each of a user's orders, oldest first.
SELECT status FROM orders WHERE user_id = @user_id ORDER BY id;

-- query: all_statuses() -> Rows<OrderStatus>
-- Every order's status, streamed.
SELECT status FROM orders ORDER BY id;
