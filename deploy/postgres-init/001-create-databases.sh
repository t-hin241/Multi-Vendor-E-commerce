#!/bin/sh
# Creates one database per service so each service owns its own schema/data,
# as required by the microservices boundary rules (no shared tables across
# services). Runs once, on first container init, via
# docker-entrypoint-initdb.d.
set -e

for db in identity_db vendor_db catalog_db inventory_db cart_db order_db payment_db shipment_db admin_db notification_db; do
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" <<-EOSQL
	SELECT 'CREATE DATABASE $db' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '$db')\gexec
	EOSQL
done
