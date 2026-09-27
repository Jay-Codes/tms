-- Phase 27: the platform's side of SMS credit sales — the packages on sale,
-- every org's orders, the Beem bundles the platform buys, and the stock and
-- margin figures built from them. Cross-org by design (admin_* files sit
-- behind auth.RequireAdmin).

-- name: AdminListSMSCreditPackages :many
SELECT * FROM sms_credit_packages
ORDER BY active DESC, sort_order, price, id;

-- name: AdminGetSMSCreditPackage :one
SELECT * FROM sms_credit_packages WHERE id = sqlc.arg(id);

-- name: AdminCreateSMSCreditPackage :one
INSERT INTO sms_credit_packages (name, credits, price, active, sort_order, created_by_admin_id)
VALUES (sqlc.arg(name), sqlc.arg(credits)::int, sqlc.arg(price)::bigint, sqlc.arg(active),
        sqlc.arg(sort_order)::int, sqlc.narg(created_by_admin_id))
RETURNING *;

-- name: AdminUpdateSMSCreditPackage :one
UPDATE sms_credit_packages
SET name = sqlc.arg(name), credits = sqlc.arg(credits)::int, price = sqlc.arg(price)::bigint,
    active = sqlc.arg(active), sort_order = sqlc.arg(sort_order)::int
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: AdminListSMSCreditOrders :many
SELECT o.*, g.name AS org_name
FROM sms_credit_orders o
JOIN orgs g ON g.id = o.org_id
WHERE (sqlc.narg(status)::text IS NULL OR o.status = sqlc.narg(status)::text)
  AND (sqlc.narg(filter_org_id)::uuid IS NULL OR o.org_id = sqlc.narg(filter_org_id)::uuid)
ORDER BY o.created_at DESC, o.id DESC
LIMIT sqlc.arg(row_limit);

-- name: AdminCountSMSCreditOrdersByStatus :many
SELECT status, count(*)::bigint AS n
FROM sms_credit_orders
GROUP BY status;

-- name: AdminInsertPlatformSMSPurchase :one
INSERT INTO platform_sms_purchases (sms_count, cost, purchased_on, reference, note, created_by_admin_id)
VALUES (sqlc.arg(sms_count)::int, sqlc.arg(cost)::bigint, sqlc.arg(purchased_on)::date,
        sqlc.narg(reference), sqlc.arg(note), sqlc.narg(created_by_admin_id))
RETURNING *;

-- name: AdminListPlatformSMSPurchases :many
SELECT p.*, COALESCE(u.full_name, '')::text AS admin_name
FROM platform_sms_purchases p
LEFT JOIN users u ON u.id = p.created_by_admin_id
ORDER BY p.purchased_on DESC, p.created_at DESC
LIMIT sqlc.arg(row_limit);

-- AdminSMSStock gathers the stock page's figures in one read:
--   sms_bought / beem_cost — every Beem bundle recorded;
--   credits_debited        — credits taken for sends (each = one segment out);
--   uncharged_sent         — sent messages no credit paid for (the exempt
--                            kinds, OTP by default), counted one segment each;
--   credits_unused         — what every org still holds: the liability;
--   credits_purchased / credits_granted — how the orgs came by them.
-- name: AdminSMSStock :one
SELECT
    (SELECT COALESCE(sum(sms_count), 0) FROM platform_sms_purchases)::bigint AS sms_bought,
    (SELECT COALESCE(sum(cost), 0) FROM platform_sms_purchases)::bigint AS beem_cost,
    (SELECT COALESCE(-sum(delta), 0) FROM sms_credit_ledger WHERE reason = 'debit')::bigint AS credits_debited,
    (SELECT count(*) FROM notification_log n
      WHERE n.status = 'sent'
        AND NOT EXISTS (SELECT 1 FROM sms_credit_ledger l
                        WHERE l.notification_id = n.id AND l.reason = 'debit'))::bigint AS uncharged_sent,
    (SELECT COALESCE(sum(balance), 0) FROM org_sms_credits)::bigint AS credits_unused,
    (SELECT COALESCE(sum(delta), 0) FROM sms_credit_ledger WHERE reason = 'purchase')::bigint AS credits_purchased,
    (SELECT COALESCE(sum(delta), 0) FROM sms_credit_ledger WHERE reason IN ('topup', 'adjust'))::bigint AS credits_granted;

-- AdminSMSSales totals completed orders credited in [from, to).
-- name: AdminSMSSales :one
SELECT count(*)::bigint AS orders,
       COALESCE(sum(amount), 0)::bigint AS sales,
       COALESCE(sum(credits), 0)::bigint AS credits_sold
FROM sms_credit_orders
WHERE status = 'completed'
  AND credited_at >= sqlc.arg(from_at)::timestamptz
  AND credited_at < sqlc.arg(to_at)::timestamptz;

-- name: AdminSMSSalesByPackage :many
SELECT package_name,
       count(*)::bigint AS orders,
       COALESCE(sum(amount), 0)::bigint AS sales,
       COALESCE(sum(credits), 0)::bigint AS credits_sold
FROM sms_credit_orders
WHERE status = 'completed'
  AND credited_at >= sqlc.arg(from_at)::timestamptz
  AND credited_at < sqlc.arg(to_at)::timestamptz
GROUP BY package_name
ORDER BY sales DESC, package_name;
