package repository_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/review/internal/adapter"
	"shopee/backend/services/review/internal/domain"
	"shopee/backend/services/review/internal/repository"
	"shopee/backend/services/review/internal/usecase"
)

// orders answers eligibility like Order: only items of completed vendor
// orders of that buyer.
type orders struct {
	items map[string][]adapter.EligibleOrderItem // by buyer
	down  bool
}

func (o *orders) ListEligible(_ context.Context, buyerID, productID string) ([]adapter.EligibleOrderItem, error) {
	if o.down {
		return nil, adapter.ErrOrderUnavailable(errors.New("order unreachable"))
	}
	out := []adapter.EligibleOrderItem{}
	for _, it := range o.items[buyerID] {
		if productID == "" || it.ProductID == productID {
			out = append(out, it)
		}
	}
	return out, nil
}

type shops struct{ owner map[string]string } // vendor -> user

func (s shops) EnsureOwnedApproved(_ context.Context, userID, vendorID, _ string) error {
	if s.owner[vendorID] != userID {
		return apperror.Forbidden("You do not own this shop")
	}
	return nil
}

type names struct {
	full map[string]string
	down bool
}

func (n *names) DisplayName(_ context.Context, userID string) (string, error) {
	if n.down {
		return "", errors.New("identity unreachable")
	}
	return n.full[userID], nil
}

type objects struct {
	mu         sync.Mutex
	stored     map[string]bool
	failUpload bool
	failDelete bool
}

func (o *objects) Upload(_ context.Context, key string, _ []byte, _ string) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.failUpload {
		return "", errors.New("storage unavailable")
	}
	o.stored[key] = true
	return "https://cdn.example.invalid/" + key, nil
}

func (o *objects) Delete(_ context.Context, key string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.failDelete {
		return errors.New("storage unavailable")
	}
	delete(o.stored, key)
	return nil
}

func (o *objects) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.stored)
}

type admins struct{ ids map[string]bool }

func (a admins) RequireRole(_ context.Context, userID, _ string) error {
	if !a.ids[userID] {
		return apperror.Forbidden("Active account with required role needed")
	}
	return nil
}

type world struct {
	pool                  *pgxpool.Pool
	uc                    *usecase.ReviewUseCase
	orders                *orders
	names                 *names
	store                 *objects
	buyer, otherBuyer     string
	shopA, shopB          string
	ownerA, ownerB, admin string
	product               string
	item, otherBuyersItem adapter.EligibleOrderItem
}

func newWorld(t *testing.T) *world {
	pool := reviewDB(t)
	w := &world{pool: pool, buyer: uuid.NewString(), otherBuyer: uuid.NewString(), shopA: uuid.NewString(), shopB: uuid.NewString(),
		ownerA: uuid.NewString(), ownerB: uuid.NewString(), admin: uuid.NewString(), product: uuid.NewString()}
	w.item = adapter.EligibleOrderItem{OrderItemID: uuid.NewString(), VendorOrderID: uuid.NewString(), ProductID: w.product, VendorID: w.shopA}
	w.otherBuyersItem = adapter.EligibleOrderItem{OrderItemID: uuid.NewString(), VendorOrderID: uuid.NewString(), ProductID: w.product, VendorID: w.shopA}
	w.orders = &orders{items: map[string][]adapter.EligibleOrderItem{w.buyer: {w.item}, w.otherBuyer: {w.otherBuyersItem}}}
	w.names = &names{full: map[string]string{w.buyer: "Nguyễn Văn An", w.otherBuyer: "Trần Thị Bích"}}
	w.store = &objects{stored: map[string]bool{}}
	w.uc = usecase.NewReviewUseCase(usecase.Deps{Repo: repository.NewReviewRepository(pool), Tx: repository.Transactions{Pool: pool},
		Orders: w.orders, Vendors: shops{owner: map[string]string{w.shopA: w.ownerA, w.shopB: w.ownerB}}, Identity: w.names,
		Store: w.store, Roles: admins{ids: map[string]bool{w.admin: true}}, Log: zerolog.Nop()})
	return w
}

func (w *world) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := w.pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func status(err error) int {
	var app *apperror.Error
	if errors.As(err, &app) {
		return app.Status
	}
	return 0
}

func (w *world) review(t *testing.T) *domain.Review {
	t.Helper()
	v, err := w.uc.Create(t.Context(), w.buyer, w.item.OrderItemID, 5, "Hàng tốt")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (w *world) reason(t *testing.T) string {
	t.Helper()
	r, err := w.uc.CreateReason(t.Context(), w.admin, "abuse", "Lời lẽ xúc phạm", nil)
	if err != nil {
		t.Fatal(err)
	}
	return r.ID
}

// REV-02: only an item Order confirms for this buyer; once per item, also
// under concurrent requests; Order down decides nothing.
func TestCreateNeedsPurchaseProofOncePerItem(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	if _, err := w.uc.Create(ctx, w.buyer, uuid.NewString(), 5, "Không mua"); status(err) != http.StatusForbidden {
		t.Fatalf("an item not bought (or not completed) must be refused, got %v", err)
	}
	if _, err := w.uc.Create(ctx, w.buyer, w.otherBuyersItem.OrderItemID, 5, "Của người khác"); status(err) != http.StatusForbidden {
		t.Fatalf("another buyer's item must be refused, got %v", err)
	}
	w.orders.down = true
	if _, err := w.uc.Create(ctx, w.buyer, w.item.OrderItemID, 5, "Order down"); status(err) != http.StatusServiceUnavailable {
		t.Fatalf("Order unavailable must be 503, got %v", err)
	}
	w.orders.down = false

	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := w.uc.Create(ctx, w.buyer, w.item.OrderItemID, 4, "Đồng thời")
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	created, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			created++
		case status(err) == http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("unexpected error %v", err)
		}
	}
	if created != 1 || conflicts != 7 || w.count(t, `SELECT count(*) FROM reviews`) != 1 {
		t.Fatalf("expected exactly one review, got %d created, %d conflicts", created, conflicts)
	}
	if left, err := w.uc.Eligibility(ctx, w.buyer, ""); err != nil || len(left) != 0 {
		t.Fatalf("a reviewed item is no longer offered: %v %v", left, err)
	}
	stored, _ := w.uc.ListMine(ctx, w.buyer, 10, 0)
	v := stored.Reviews[0]
	if !v.VerifiedPurchase || v.AuthorLabel == nil || *v.AuthorLabel != "N***n" || v.VendorID != w.shopA {
		t.Fatalf("expected a verified review with a masked label, got %+v", v)
	}
}

// REV-03: Identity down still lets the buyer review; the public label is
// generic until the backfill resolves it. REV-05: seeded rows are never
// verified and are not shown (or counted) unless demo data is enabled.
func TestPublicListIsSafeAndCountsWhatItShows(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	w.names.down = true
	v := w.review(t)
	if v.AuthorLabel != nil {
		t.Fatal("no label without Identity")
	}
	if _, err := w.pool.Exec(ctx, `INSERT INTO reviews (buyer_id, vendor_id, product_id, order_item_id, vendor_order_id, rating, comment)
		VALUES ($1, $2, $3, $4, $5, 1, 'Seeded')`, uuid.NewString(), w.shopA, w.product, uuid.NewString(), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	p, s, err := w.uc.ListPublic(ctx, w.product, 0, 20, 0)
	if err != nil || len(p.Reviews) != 1 || s.RatingCount != 1 || s.RatingAverage != 5 {
		t.Fatalf("only the verified review is shown and counted: %d %+v %v", len(p.Reviews), s, err)
	}
	if _, _, err := w.uc.ListPublic(ctx, "not-a-uuid", 0, 20, 0); status(err) != http.StatusBadRequest {
		t.Fatalf("an invalid product id is a validation error, got %v", err)
	}
	w.names.down = false
	if n, err := w.uc.BackfillLabels(ctx, 10); err != nil || n != 2 {
		t.Fatalf("backfill: %d %v", n, err)
	}
	p, _, _ = w.uc.ListPublic(ctx, w.product, 0, 20, 0)
	if p.Reviews[0].AuthorLabel == nil || *p.Reviews[0].AuthorLabel != "N***n" {
		t.Fatal("the backfill sets the masked label")
	}
	demo := usecase.NewReviewUseCase(usecase.Deps{Repo: repository.NewReviewRepository(w.pool), Log: zerolog.Nop(), ShowUnverified: true})
	if p, s, err := demo.ListPublic(ctx, w.product, 0, 20, 0); err != nil || len(p.Reviews) != 2 || s.RatingCount != 2 {
		t.Fatalf("demo mode shows seeded reviews: %v %v", s, err)
	}
}

// REV-04: shop B cannot reply to or report shop A's review; shop A's
// reply and report are audited; a repeated report is refused.
func TestShopActionsNeedOwnershipAndAreAudited(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	v := w.review(t)
	reason := w.reason(t)
	if _, err := w.uc.Reply(ctx, w.ownerB, w.shopB, v.ID, "Không phải shop của tôi"); status(err) != http.StatusForbidden {
		t.Fatalf("another shop's reply must be refused, got %v", err)
	}
	if _, err := w.uc.Reply(ctx, w.ownerB, w.shopA, v.ID, "Mạo danh"); status(err) != http.StatusForbidden {
		t.Fatalf("a user who does not own the shop must be refused, got %v", err)
	}
	if _, err := w.uc.Report(ctx, w.ownerB, w.shopB, v.ID, reason, nil); status(err) != http.StatusForbidden {
		t.Fatalf("another shop's report must be refused, got %v", err)
	}
	if _, err := w.uc.Reply(ctx, w.ownerA, w.shopA, v.ID, "Cảm ơn bạn"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.uc.Reply(ctx, w.ownerA, w.shopA, v.ID, "Cảm ơn bạn nhiều"); err != nil {
		t.Fatal(err)
	}
	note := "Ngôn từ không phù hợp"
	if _, err := w.uc.Report(ctx, w.ownerA, w.shopA, v.ID, reason, &note); err != nil {
		t.Fatal(err)
	}
	if _, err := w.uc.Report(ctx, w.ownerA, w.shopA, v.ID, reason, nil); status(err) != http.StatusConflict {
		t.Fatalf("a second open report must conflict, got %v", err)
	}
	if n := w.count(t, `SELECT count(*) FROM review_moderation_audit_logs WHERE entity_id = $1 AND actor_id = $2
		AND action IN ('reply_created', 'reply_updated', 'report_created')`, v.ID, w.ownerA); n != 3 {
		t.Fatalf("expected three audited shop actions, got %d", n)
	}
}

// REV-04: hide through a report, restore, direct hide: admin verified,
// list and summary follow, everything audited and audit is append-only.
func TestModerationUpdatesStorefrontAndAudit(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	v := w.review(t)
	reason := w.reason(t)
	report, err := w.uc.Report(ctx, w.ownerA, w.shopA, v.ID, reason, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.uc.ResolveReport(ctx, w.buyer, report.ID, domain.DecisionHide, reason, nil); status(err) != http.StatusForbidden {
		t.Fatalf("a non-admin must be refused, got %v", err)
	}
	if err := w.uc.ResolveReport(ctx, w.admin, report.ID, domain.DecisionHide, "", nil); status(err) != http.StatusBadRequest {
		t.Fatalf("hiding needs a reason, got %v", err)
	}
	if err := w.uc.ResolveReport(ctx, w.admin, report.ID, domain.DecisionHide, reason, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.uc.ResolveReport(ctx, w.admin, report.ID, domain.DecisionKeep, "", nil); status(err) != http.StatusConflict {
		t.Fatalf("a resolved report cannot be resolved again, got %v", err)
	}
	p, s, _ := w.uc.ListPublic(ctx, w.product, 0, 20, 0)
	if len(p.Reviews) != 0 || s.RatingCount != 0 {
		t.Fatal("a hidden review leaves the list and the summary")
	}
	if _, err := w.uc.Reply(ctx, w.ownerA, w.shopA, v.ID, "Sau khi ẩn"); status(err) != http.StatusConflict {
		t.Fatalf("a hidden review cannot be replied to, got %v", err)
	}
	if err := w.uc.Restore(ctx, w.admin, v.ID, nil); status(err) != http.StatusBadRequest {
		t.Fatalf("restoring needs a note, got %v", err)
	}
	note := "Báo cáo nhầm"
	if err := w.uc.Restore(ctx, w.admin, v.ID, &note); err != nil {
		t.Fatal(err)
	}
	if _, s, _ := w.uc.ListPublic(ctx, w.product, 0, 20, 0); s.RatingCount != 1 {
		t.Fatal("a restored review counts again")
	}
	if err := w.uc.Hide(ctx, w.admin, v.ID, reason, &note); err != nil {
		t.Fatal(err)
	}
	if err := w.uc.Hide(ctx, w.admin, v.ID, reason, &note); status(err) != http.StatusConflict {
		t.Fatalf("hiding twice conflicts, got %v", err)
	}
	for action, want := range map[string]int{"report_hide": 1, "review_restored": 1, "review_hidden": 1, "reason_created": 1} {
		if n := w.count(t, `SELECT count(*) FROM review_moderation_audit_logs WHERE action = $1 AND actor_id = $2`, action, w.admin); n != want {
			t.Errorf("%s: %d audit rows, want %d", action, n, want)
		}
	}
	if _, err := w.pool.Exec(ctx, `DELETE FROM review_moderation_audit_logs`); err == nil {
		t.Fatal("audit rows must not be deletable")
	}
	if n := w.count(t, `SELECT count(*) FROM (`+repository.AuditSearchSQL+`) a(id, at, actor, action, entity_type, entity_id, note, request_id, changes)
		WHERE entity_type = 'review' AND entity_id = $1`, v.ID); n != 4 {
		t.Fatalf("the admin audit search sees the review's history, got %d", n)
	}
	counts, err := w.uc.Operations(ctx, w.admin)
	if err != nil || counts["hidden_7d"] != 1 || counts["open_reports"] != 0 {
		t.Fatalf("operations: %v %v", counts, err)
	}
}

func photo(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// REV-04: only the author uploads, at most five even concurrently; a
// failed store or metadata write leaves no object behind for good.
func TestImageUploadsAreBoundedAndCleanedUp(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	v := w.review(t)
	img := photo(t)
	if _, err := w.uc.UploadImage(ctx, w.otherBuyer, v.ID, "image/jpeg", img); status(err) != http.StatusNotFound {
		t.Fatalf("another buyer cannot add photos, got %v", err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := w.uc.UploadImage(ctx, w.buyer, v.ID, "image/jpeg", img); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 5 || w.count(t, `SELECT count(*) FROM review_images WHERE review_id = $1`, v.ID) != 5 || w.store.count() != 5 {
		t.Fatalf("expected five photos, got %d stored rows and %d objects", ok, w.store.count())
	}
	if w.count(t, `SELECT count(DISTINCT position) FROM review_images WHERE review_id = $1`, v.ID) != 5 {
		t.Fatal("positions must be distinct")
	}

	// A second review whose store fails: nothing recorded, nothing left.
	w.orders.items[w.buyer] = append(w.orders.items[w.buyer], adapter.EligibleOrderItem{OrderItemID: uuid.NewString(), VendorOrderID: uuid.NewString(), ProductID: w.product, VendorID: w.shopA})
	second, err := w.uc.Create(ctx, w.buyer, w.orders.items[w.buyer][1].OrderItemID, 4, "Lần hai")
	if err != nil {
		t.Fatal(err)
	}
	w.store.failUpload = true
	if _, err := w.uc.UploadImage(ctx, w.buyer, second.ID, "image/jpeg", img); err == nil {
		t.Fatal("a failed store must fail the upload")
	}
	w.store.failUpload = false
	if w.count(t, `SELECT count(*) FROM review_image_uploads`) != 0 {
		t.Fatal("a failed upload is closed at once when its object can be removed")
	}

	// An upload left behind (process stopped after storing the object):
	// cleanup retries while storage refuses, then removes it.
	key := "reviews/" + second.ID + "/left-behind.jpg"
	w.store.stored[key] = true
	if _, err := w.pool.Exec(ctx, `INSERT INTO review_image_uploads (review_id, object_key, next_attempt_at) VALUES ($1, $2, now() - interval '1 minute')`, second.ID, key); err != nil {
		t.Fatal(err)
	}
	w.store.failDelete = true
	if n, err := w.uc.CleanUploads(ctx); err != nil || n != 0 {
		t.Fatalf("cleanup while storage refuses: %d %v", n, err)
	}
	if w.count(t, `SELECT attempts FROM review_image_uploads WHERE object_key = $1`, key) != 1 {
		t.Fatal("a failed cleanup is retried later")
	}
	w.store.failDelete = false
	if _, err := w.pool.Exec(ctx, `UPDATE review_image_uploads SET next_attempt_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if n, err := w.uc.CleanUploads(ctx); err != nil || n != 1 || w.store.stored[key] {
		t.Fatalf("expected the object removed: %d %v", n, err)
	}
	if w.store.count() != 5 {
		t.Fatal("photos in use are never removed by the cleanup")
	}
	if _, err := w.uc.UploadImage(ctx, w.buyer, second.ID, "image/png", img); status(err) != http.StatusBadRequest {
		t.Fatalf("a mislabelled file is refused, got %v", err)
	}
}

// Migration 000003 on data written before it: rows are not verified, the
// audit keeps its history with the new columns filled.
func TestHardeningMigrationOnExistingRows(t *testing.T) {
	pool := reviewDB(t)
	ctx := t.Context()
	var label *string
	var verified bool
	if _, err := pool.Exec(ctx, `INSERT INTO reviews (buyer_id, vendor_id, product_id, order_item_id, vendor_order_id, rating, comment)
		VALUES ($1, $1, $1, $1, $1, 3, 'x')`, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT verified_purchase, author_label FROM reviews`).Scan(&verified, &label); err != nil || verified || label != nil {
		t.Fatalf("rows written without Review's check are not verified: %v %v", verified, err)
	}
	// A writer that does not know entity_id still records a complete row.
	var review string
	_ = pool.QueryRow(ctx, `SELECT id FROM reviews`).Scan(&review)
	if _, err := pool.Exec(ctx, `INSERT INTO review_moderation_audit_logs (review_id, actor_id, action) VALUES ($1, $2, 'report_keep')`, review, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	var entity string
	if err := pool.QueryRow(ctx, `SELECT entity_id FROM review_moderation_audit_logs`).Scan(&entity); err != nil || entity != review {
		t.Fatalf("entity_id filled from review_id: %q %v", entity, err)
	}
	if !strings.Contains(repository.AuditSearchSQL, "entity_type") {
		t.Fatal("audit search exposes the entity type")
	}
}
