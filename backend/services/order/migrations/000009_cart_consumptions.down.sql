-- Only safe once no pending/held rows remain (see deploy/cart-runbook.md);
-- dropping the table loses the record of which carts still need tidying.
DROP TABLE IF EXISTS cart_consumptions;
