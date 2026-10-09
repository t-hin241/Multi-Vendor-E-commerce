package repository_test

import (
	"testing"

	"shopee/backend/services/order/internal/repository"
)

// PW-008: every operator queue reads on the real schema and an empty
// database needs nobody.
func TestWorkQueuesReadOnTheSchema(t *testing.T) {
	pool := orderDB(t)
	for _, q := range repository.WorkQueues {
		var items int64
		var oldest float64
		if err := pool.QueryRow(t.Context(), q.SQL).Scan(&items, &oldest); err != nil {
			t.Fatalf("%s: %v", q.Name, err)
		}
		if q.Severity != "critical" && q.Severity != "warning" {
			t.Fatalf("%s: severity %q", q.Name, q.Severity)
		}
		if items != 0 || oldest != 0 {
			t.Fatalf("%s: an empty database needs nobody, got %d (%vs)", q.Name, items, oldest)
		}

	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO orders(id,buyer_id,total_amount,subtotal_amount,currency,recipient_name,phone,province,district,ward,street_address)
		VALUES(gen_random_uuid(),gen_random_uuid(),1,1,'VND','R','0000000000','P','D','W','S')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO order_effects(order_id, kind, target, status) SELECT id, 'notify', 'x', 'parked' FROM orders LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	var parked int64
	var oldest float64
	if err := pool.QueryRow(t.Context(), repository.WorkQueues[0].SQL).Scan(&parked, &oldest); err != nil || parked != 1 || repository.WorkQueues[0].Name != "order_effects_parked" {
		t.Fatalf("a parked effect needs a person: %d %v", parked, err)
	}
}
