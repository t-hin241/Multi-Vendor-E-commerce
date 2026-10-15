package repository_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/bulkimport"
	"shopee/backend/services/inventory/internal/domain"
	"shopee/backend/services/inventory/internal/repository"
	"shopee/backend/services/inventory/internal/usecase"
)

type importFiles struct {
	mu    sync.Mutex
	files map[string][]byte
}

func (m *importFiles) Put(_ context.Context, key string, data []byte, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.files == nil {
		m.files = map[string][]byte{}
	}
	m.files[key] = append([]byte(nil), data...)
	return nil
}
func (m *importFiles) PutStream(ctx context.Context, key string, r io.Reader, ct string) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	return m.Put(ctx, key, b, ct)
}
func (m *importFiles) Open(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.files[key]
	if !ok {
		return nil, errors.New("missing object")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (m *importFiles) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, key)
	return nil
}
func (m *importFiles) DownloadFileURL(_ context.Context, key, _ string, _ time.Duration) (string, error) {
	return "memory://" + key, nil
}

// fakeCatalog answers like Catalog: only the shop's own SKUs and products.
type fakeCatalog struct {
	vendor  string
	targets []domain.CatalogTarget
}

func (c fakeCatalog) VendorTargets(_ context.Context, vendor string, skus, variantIDs, productIDs []string) ([]domain.CatalogTarget, error) {
	var out []domain.CatalogTarget
	if vendor != c.vendor {
		return out, nil
	}
	in := func(list []string, v string) bool {
		for _, x := range list {
			if x == v && v != "" {
				return true
			}
		}
		return false
	}
	for _, t := range c.targets {
		if (t.VariantID != "" && (in(skus, t.SKU) || in(variantIDs, t.VariantID))) || (t.VariantID == "" && in(productIDs, t.ProductID)) {
			out = append(out, t)
		}
	}
	return out, nil
}

type revocableStockVendor struct {
	mu            sync.Mutex
	vendor, actor string
	revoked       bool
}

func (v *revocableStockVendor) GetApprovedVendorID(_ context.Context, actor, vendor, _ string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.revoked || actor != v.actor || vendor != v.vendor {
		return "", apperror.Forbidden("Not a member")
	}
	return vendor, nil
}

type receiptFixture struct {
	pool          *pgxpool.Pool
	vendor, actor string
	stock         usecase.StockSelfService
	svc           *bulkimport.Service
	members       *revocableStockVendor
	catalog       *fakeCatalog
}

func newReceiptFixture(t *testing.T) *receiptFixture {
	pool := inventoryDB(t)
	f := &receiptFixture{pool: pool, vendor: uuid.NewString(), actor: uuid.NewString()}
	f.members = &revocableStockVendor{vendor: f.vendor, actor: f.actor}
	f.stock = usecase.StockSelfService{Enabled: true, Store: repository.StockSelfService{Pool: pool}, Items: repository.NewInventoryItemRepository(pool), Vendors: f.members,
		Ops: usecase.Operations{Transactions: repository.Transactions{Pool: pool}, Identity: stockAdmin{}, Audit: repository.AdminAudit{Pool: pool}}}
	f.catalog = &fakeCatalog{vendor: f.vendor}
	f.svc = &bulkimport.Service{Owner: "inventory", Store: repository.ImportStore(pool), Files: &importFiles{}, Authorize: f.stock.BulkAuthorizer(), Enabled: true,
		MaxRows: 1000, MaxBytes: 5 << 20, Log: zerolog.Nop(),
		Kinds: map[string]bulkimport.Kind{"stock_receipts": usecase.StockReceiptImport{Stock: f.stock, Catalog: f.catalog, Items: repository.BulkItems{Pool: pool}}}}
	return f
}

// item creates stock of the shop and registers it with the fake Catalog.
func (f *receiptFixture) item(t *testing.T, sku string, quantity int64) *domain.InventoryItem {
	t.Helper()
	i := &domain.InventoryItem{ProductID: uuid.NewString(), VendorID: f.vendor, ActorUserID: f.actor, AvailableQuantity: quantity}
	target := domain.CatalogTarget{ProductID: i.ProductID, ProductName: "Sản phẩm " + sku}
	if sku != "" {
		id := uuid.NewString()
		i.VariantID = &id
		target.VariantID, target.SKU, target.HasVariants = id, sku, true
	}
	if err := repository.NewInventoryItemRepository(f.pool).Create(t.Context(), i); err != nil {
		t.Fatal(err)
	}
	f.catalog.targets = append(f.catalog.targets, target)
	return i
}

func (f *receiptFixture) preview(t *testing.T, csv string) *bulkimport.Job {
	t.Helper()
	j, err := f.svc.Upload(t.Context(), f.actor, uuid.NewString(), bulkimport.UploadInput{VendorID: f.vendor, Kind: "stock_receipts", TemplateVersion: 1, FileName: "nhap.csv", Data: []byte(csv)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.ValidateNext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if j, err = f.svc.Job(t.Context(), f.actor, j.ID); err != nil || j.Status != bulkimport.StatusPreviewReady {
		t.Fatalf("preview %+v %v", j, err)
	}
	return j
}

func (f *receiptFixture) rows(t *testing.T, job string) map[int]bulkimport.Row {
	t.Helper()
	p, err := f.svc.Rows(t.Context(), f.actor, job, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	out := map[int]bulkimport.Row{}
	for _, r := range p.Items {
		out[r.RowNo] = r
	}
	return out
}

func (f *receiptFixture) drain(t *testing.T) {
	t.Helper()
	for range 100 {
		more, err := f.svc.ApplyNext(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			return
		}
	}
	t.Fatal("apply did not drain")
}

func (f *receiptFixture) confirm(t *testing.T, j *bulkimport.Job) {
	t.Helper()
	if _, err := f.svc.Confirm(t.Context(), f.actor, j.ID, bulkimport.ConfirmInput{PreviewHash: j.PreviewHash, ExpectedVersion: j.Version}); err != nil {
		t.Fatal(err)
	}
}

func available(t *testing.T, pool *pgxpool.Pool, id string) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(t.Context(), `SELECT available_quantity FROM inventory_items WHERE id=$1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestStockImportSchemaMatchesSharedCopy(t *testing.T) {
	shared, err := os.ReadFile("../../../../pkg/bulkimport/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../migrations/000011_bulk_operations.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(migration), string(shared)[strings.Index(string(shared), "CREATE TABLE import_jobs"):]) {
		t.Fatal("migration copy of the import job schema drifted from pkg/bulkimport/schema.sql")
	}
}

func TestStockImportNeedsSelfServicePolicy(t *testing.T) {
	f := newReceiptFixture(t)
	tmpl, err := f.svc.Template(t.Context(), f.actor, f.vendor, "stock_receipts")
	if err != nil || tmpl.Enabled || tmpl.DisabledReason == "" {
		t.Fatalf("template %+v %v", tmpl, err)
	}
	_, err = f.svc.Upload(t.Context(), f.actor, uuid.NewString(), bulkimport.UploadInput{VendorID: f.vendor, Kind: "stock_receipts", TemplateVersion: 1, Data: []byte("sku,quantity,reason,evidence_ref\nA,1,abc,x\n")})
	var app *apperror.Error
	if !errors.As(err, &app) || app.Code != "bulk_operations_disabled" {
		t.Fatalf("upload with AF-13 off: %v", err)
	}
	if _, err = f.svc.Template(t.Context(), uuid.NewString(), f.vendor, "stock_receipts"); err == nil {
		t.Fatal("non-member read the template")
	}
}

func TestStockImportReceiptsApprovalStaleAndLedger(t *testing.T) {
	f := newReceiptFixture(t)
	ctx := t.Context()
	enableStock(t, f.stock, f.vendor, 100)
	red := f.item(t, "ÁO-ĐỎ", 5)
	blue := f.item(t, "ÁO-XANH", 5)
	plain := f.item(t, "", 1)
	busy := f.item(t, "BUSY", 10)
	f.catalog.targets = append(f.catalog.targets, domain.CatalogTarget{ProductID: uuid.NewString(), HasVariants: true})
	withSKUs := f.catalog.targets[len(f.catalog.targets)-1].ProductID
	noStock := uuid.NewString()
	f.catalog.targets = append(f.catalog.targets, domain.CatalogTarget{ProductID: noStock})
	csv := "sku,product_id,quantity,reason,evidence_ref,available\n" +
		"ÁO-ĐỎ,,20,Nhập hàng đợt 1,PN-001,999\n" + // valid
		"ÁO-XANH,,150,Nhập hàng đợt 1,PN-001,\n" + // over limit: approval
		fmt.Sprintf(",%s,3,Nhập hàng đợt 1,PN-002,\n", plain.ProductID) + // product without SKUs
		"BUSY,,4,Nhập hàng đợt 1,PN-003,\n" + // changes after preview
		"KHONG-CO,,1,Nhập hàng đợt 1,PN-004,\n" + // unknown SKU
		fmt.Sprintf(",%s,1,Nhập hàng đợt 1,PN-005,\n", withSKUs) + // needs SKUs
		fmt.Sprintf(",%s,1,Nhập hàng đợt 1,PN-006,\n", noStock) + // no stock record
		"ÁO-ĐỎ,,1.5,Nhập hàng đợt 1,PN-007,\n" + // decimal
		"ÁO-ĐỎ,,99999999999999999999,Nhập hàng đợt 1,PN-008,\n" + // overflow
		"ÁO-ĐỎ,,1,ab,PN-009,\n" + // short reason
		"ÁO-ĐỎ,,-1,Nhập hàng,PN-010,\n" // negative
	j := f.preview(t, csv)
	rows := f.rows(t, j.ID)
	for n, want := range map[int]string{1: "valid", 2: "valid", 3: "valid", 4: "valid", 5: "target_not_found", 6: "use_sku", 7: "no_stock_item", 8: "invalid_quantity", 9: "invalid_quantity", 10: "invalid_value", 11: "unsafe_value"} {
		got := rows[n].Status
		if rows[n].Error != nil {
			got = rows[n].Error.Code
		}
		if got != want {
			t.Fatalf("row %d: %s %+v", n, got, rows[n].Error)
		}
	}
	if rows[2].New["needs_approval"] != true || rows[1].New["available"] != float64(25) {
		t.Fatalf("diff %+v %+v", rows[1].New, rows[2].New)
	}
	if available(t, f.pool, red.ID) != 5 {
		t.Fatal("dry-run changed stock")
	}
	// An order reserves BUSY after the preview: its row must fail, not
	// add stock on top of a version the vendor never saw.
	if _, err := repository.NewReservationRepository(f.pool).ReserveAtomic(ctx, uuid.NewString(), []domain.ReservationLine{line(busy, 2)}); err != nil {
		t.Fatal(err)
	}
	f.confirm(t, j)
	f.drain(t)
	rows = f.rows(t, j.ID)
	if rows[1].Status != "succeeded" || rows[2].Status != "needs_approval" || rows[3].Status != "succeeded" || rows[4].Status != "failed" || rows[4].Error.Code != "preview_stale" {
		t.Fatalf("applied %+v %+v %+v %+v", rows[1], rows[2], rows[3], rows[4])
	}
	if available(t, f.pool, red.ID) != 25 || available(t, f.pool, blue.ID) != 5 || available(t, f.pool, plain.ID) != 4 || available(t, f.pool, busy.ID) != 8 {
		t.Fatal("wrong stock after import")
	}
	var movements int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM stock_movements WHERE inventory_item_id=$1 AND source='vendor_self_service'`, red.ID).Scan(&movements); err != nil || movements != 1 {
		t.Fatalf("movements %d %v", movements, err)
	}
	done, err := f.svc.Job(ctx, f.actor, j.ID)
	if err != nil || done.Summary.Succeeded+done.Summary.NeedsApproval+done.Summary.Failed+done.Summary.Invalid != done.Summary.Total || done.Outcome != "partial" {
		t.Fatalf("summary %+v %v", done, err)
	}
	assertBalance(t, f.pool)
}

func TestStockImportReplaysAppliedOperationAfterCrash(t *testing.T) {
	f := newReceiptFixture(t)
	ctx := t.Context()
	enableStock(t, f.stock, f.vendor, 100)
	red := f.item(t, "RED", 5)
	j := f.preview(t, "sku,quantity,reason,evidence_ref\nRED,7,Nhập hàng đợt 2,PN-100\n")
	row := f.rows(t, j.ID)[1]
	f.confirm(t, j)
	// The receipt committed under the row's operation reference but the
	// worker died before recording the row (an older deployment, or a
	// manual replay): applying the row again must return the same movement.
	var a domain.StockAdjustment
	if err := json.Unmarshal(row.Payload, &a); err != nil {
		t.Fatal(err)
	}
	first, err := f.stock.Adjust(ctx, f.actor, row.OperationRef, a)
	if err != nil || first.Status != "applied" {
		t.Fatalf("first %+v %v", first, err)
	}
	f.drain(t)
	row = f.rows(t, j.ID)[1]
	if row.Status != "succeeded" || row.ResultRef != "movement:"+first.MovementID || available(t, f.pool, red.ID) != 12 {
		t.Fatalf("replay %+v stock %d", row, available(t, f.pool, red.ID))
	}
	assertBalance(t, f.pool)
}

func TestStockImportStopsWhenPermissionRevoked(t *testing.T) {
	f := newReceiptFixture(t)
	ctx := t.Context()
	enableStock(t, f.stock, f.vendor, 100)
	a, b, c := f.item(t, "A1", 1), f.item(t, "B1", 1), f.item(t, "C1", 1)
	j := f.preview(t, "sku,quantity,reason,evidence_ref\nA1,1,Nhập hàng,PN\nB1,1,Nhập hàng,PN\nC1,1,Nhập hàng,PN\n")
	f.confirm(t, j)
	if _, err := f.svc.ApplyNext(ctx); err != nil {
		t.Fatal(err)
	}
	f.members.mu.Lock()
	f.members.revoked = true
	f.members.mu.Unlock()
	f.drain(t)
	stopped, err := repository.ImportStore(f.pool).Job(ctx, j.ID)
	if err != nil || stopped.ErrorCode != "permission_revoked" || stopped.Summary.Succeeded != 1 || stopped.Summary.Failed != 1 || stopped.Summary.Cancelled != 1 {
		t.Fatalf("stopped %+v %v", stopped, err)
	}
	if available(t, f.pool, a.ID) != 2 || available(t, f.pool, b.ID) != 1 || available(t, f.pool, c.ID) != 1 {
		t.Fatal("stock after revocation")
	}
	// Jobs of the shop are no longer visible to the former member.
	if _, err = f.svc.Job(ctx, f.actor, j.ID); err == nil {
		t.Fatal("revoked member still reads the job")
	}
}

func TestStockImportExportListsShopItems(t *testing.T) {
	f := newReceiptFixture(t)
	ctx := t.Context()
	enableStock(t, f.stock, f.vendor, 100)
	f.item(t, "EXP-1", 3)
	plain := f.item(t, "", 4)
	e, err := f.svc.CreateExport(ctx, f.actor, f.vendor, "stock_receipts", "stock-export-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.ExportNext(ctx); err != nil {
		t.Fatal(err)
	}
	d, err := f.svc.ExportDownload(ctx, f.actor, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	body := f.svc.Files.(*importFiles).files[strings.TrimPrefix(d.URL, "memory://")]
	if !strings.Contains(string(body), "sku,product_id,quantity,reason,evidence_ref,product_name,available,reserved\n") ||
		!strings.Contains(string(body), "EXP-1,") || !strings.Contains(string(body), ","+plain.ProductID+",,,,Sản phẩm ,4,0\n") {
		t.Fatalf("export %q", body)
	}
}
