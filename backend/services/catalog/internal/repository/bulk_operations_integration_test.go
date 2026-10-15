package repository_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/bulkimport"
	"shopee/backend/services/catalog/internal/domain"
	"shopee/backend/services/catalog/internal/repository"
	"shopee/backend/services/catalog/internal/usecase"
)

// memoryFiles is a fake private bucket.
type memoryFiles struct {
	mu    sync.Mutex
	files map[string][]byte
}

func (m *memoryFiles) Put(_ context.Context, key string, data []byte, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.files == nil {
		m.files = map[string][]byte{}
	}
	m.files[key] = append([]byte(nil), data...)
	return nil
}
func (m *memoryFiles) PutStream(ctx context.Context, key string, r io.Reader, ct string) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	return m.Put(ctx, key, b, ct)
}
func (m *memoryFiles) Open(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.files[key]
	if !ok {
		return nil, errors.New("missing object")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (m *memoryFiles) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, key)
	return nil
}
func (m *memoryFiles) DownloadFileURL(_ context.Context, key, _ string, _ time.Duration) (string, error) {
	return "memory://" + key, nil
}
func (m *memoryFiles) get(key string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return string(m.files[key])
}

// revocableVendor lets a test take the shop permission away mid-job.
type revocableVendor struct {
	mu          sync.Mutex
	owner, shop string
	revoked     bool
}

func (v *revocableVendor) Approved(context.Context, []string) (map[string]int64, error) {
	return map[string]int64{v.shop: 1}, nil
}
func (v *revocableVendor) GetApprovedVendorID(_ context.Context, user, shop, _ string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.revoked || user != v.owner || shop != v.shop {
		return "", apperror.Forbidden("Not a member")
	}
	return shop, nil
}

type bulkFixture struct {
	*fixture
	vendor *revocableVendor
	files  *memoryFiles
	svc    *bulkimport.Service
}

func newBulkFixture(t *testing.T) *bulkFixture {
	f := newFixture(t)
	v := &revocableVendor{owner: f.owner, shop: f.shop}
	pool := f.pool
	cats := repository.NewCategoryRepository(pool)
	attrs := usecase.NewAttributeUseCase(repository.NewAttributeRepository(pool), repository.NewCategoryAttributeRuleRepository(pool), cats, roleStub{f.admin})
	f.uc = usecase.NewProductUseCase(f.repo, repository.NewProductImageRepository(pool), repository.NewProductMediaRepository(pool), cats, repository.NewAuditLogRepository(pool),
		v, f.store, nil, nil, repository.NewStorefrontCacheRepository(pool), attrs, repository.NewProductAttributeValueRepository(pool), repository.NewProductVariantRepository(pool),
		inventoryStub{}, repository.NewProductPackagingRepository(pool), usecase.Operations{Transactions: repository.Transactions{Pool: pool}, Cleanup: repository.ObjectCleanup{Pool: pool}, Identity: roleStub{f.admin}, Log: zerolog.Nop()})
	f.uc.VariantPricingEnabled = true
	store := repository.BulkOperations{Pool: pool}
	files := &memoryFiles{}
	svc := &bulkimport.Service{Owner: "catalog", Store: repository.ImportStore(pool), Files: files, Authorize: f.uc.BulkAuthorizer(), Enabled: true, MaxRows: 1000, MaxBytes: 5 << 20, Log: zerolog.Nop(),
		Kinds: map[string]bulkimport.Kind{"products": usecase.ProductImport{UC: f.uc, Store: store}, "prices": usecase.PriceImport{UC: f.uc, Store: store}}}
	return &bulkFixture{fixture: f, vendor: v, files: files, svc: svc}
}

func (b *bulkFixture) upload(t *testing.T, kind, csv string) *bulkimport.Job {
	t.Helper()
	j, err := b.svc.Upload(t.Context(), b.owner, uuid.NewString(), bulkimport.UploadInput{VendorID: b.shop, Kind: kind, TemplateVersion: 1, FileName: "file.csv", Data: []byte(csv)})
	if err != nil {
		t.Fatal(err)
	}
	if more, err := b.svc.ValidateNext(t.Context()); err != nil || !more {
		t.Fatalf("validate %v %v", more, err)
	}
	j, err = b.svc.Job(t.Context(), b.owner, j.ID)
	if err != nil || j.Status != bulkimport.StatusPreviewReady {
		t.Fatalf("preview %+v %v", j, err)
	}
	return j
}

func (b *bulkFixture) rows(t *testing.T, job string) map[int]bulkimport.Row {
	t.Helper()
	p, err := b.svc.Rows(t.Context(), b.owner, job, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	out := map[int]bulkimport.Row{}
	for _, r := range p.Items {
		out[r.RowNo] = r
	}
	return out
}

func (b *bulkFixture) drain(t *testing.T) {
	t.Helper()
	for range 100 {
		more, err := b.svc.ApplyNext(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			return
		}
	}
	t.Fatal("apply did not drain")
}

func checkTotals(t *testing.T, j *bulkimport.Job) {
	t.Helper()
	s := j.Summary
	if s.Invalid+s.Unchanged+s.Pending+s.Succeeded+s.NeedsApproval+s.Failed+s.Cancelled != s.Total {
		t.Fatalf("summary does not add up: %+v", s)
	}
}

func code(err error) string {
	var app *apperror.Error
	if errors.As(err, &app) {
		return string(app.Code)
	}
	return fmt.Sprint(err)
}

func TestBulkSchemaMatchesSharedCopy(t *testing.T) {
	shared, err := os.ReadFile("../../../../pkg/bulkimport/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../migrations/000018_bulk_operations.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	body := string(shared)[strings.Index(string(shared), "CREATE TABLE import_jobs"):]
	if !strings.Contains(string(migration), body) {
		t.Fatal("migration copy of the import job schema drifted from pkg/bulkimport/schema.sql")
	}
}

func TestBulkPriceImportPreviewConfirmApplyAndReport(t *testing.T) {
	b := newBulkFixture(t)
	ctx := t.Context()
	plain := b.product(t)
	withSKUs := b.product(t)
	red := pricedVariant(t, b.fixture, withSKUs, 120)
	blue := pricedVariant(t, b.fixture, withSKUs, 130)
	sellPricedProduct(t, b.fixture, withSKUs.ID)
	// A SKU of another shop in the same database looks exactly like a missing one.
	otherShop := &domain.Product{VendorID: uuid.NewString(), CategoryID: b.category, Name: "Other shop", Slug: uuid.NewString(), PriceAmount: 50, Currency: "VND"}
	if err := b.repo.Create(ctx, otherShop); err != nil {
		t.Fatal(err)
	}
	foreign := pricedVariant(t, b.fixture, otherShop, 50)
	plain, _ = b.repo.FindByID(ctx, plain.ID)
	csv := "sku,product_id,price_amount,expected_version,product_name\n" +
		fmt.Sprintf("%s,,150,%d,ignored\n", red.SKU, red.PriceVersion) + // valid
		fmt.Sprintf("%s,,130,,\n", blue.SKU) + // unchanged
		fmt.Sprintf(",%s,99,%d,\n", plain.ID, plain.Version) + // valid product price
		fmt.Sprintf("%s,,10,,\n", foreign.SKU) + // other shop: not found
		fmt.Sprintf(",%s,10,,\n", withSKUs.ID) + // product with SKUs
		fmt.Sprintf("%s,,12.5,,\n", red.SKU) + // decimal
		"X,,=1+1,,\n" + // formula
		fmt.Sprintf("%s,,140,%d,\n", blue.SKU, blue.PriceVersion+7) // stale export
	j := b.upload(t, "prices", csv)
	rows := b.rows(t, j.ID)
	want := map[int]string{1: "valid", 2: "unchanged", 3: "valid", 4: "invalid", 5: "invalid", 6: "invalid", 7: "invalid", 8: "invalid"}
	for n, status := range want {
		if rows[n].Status != status {
			t.Fatalf("row %d: %s %+v", n, rows[n].Status, rows[n].Error)
		}
	}
	if rows[4].Error.Code != "target_not_found" || rows[5].Error.Code != "use_sku" || rows[6].Error.Code != "invalid_amount" || rows[7].Error.Code != "unsafe_value" || rows[8].Error.Code != "preview_stale" {
		t.Fatalf("errors %+v %+v %+v %+v %+v", rows[4].Error, rows[5].Error, rows[6].Error, rows[7].Error, rows[8].Error)
	}
	if rows[1].New["requires_review"] != true || rows[1].Old["price_amount"] != float64(120) {
		t.Fatalf("diff %+v %+v", rows[1].Old, rows[1].New)
	}
	// Nothing changes before confirm.
	if v, _ := repository.NewProductVariantRepository(b.pool).FindByID(ctx, red.ID); v.PriceAmount != 120 {
		t.Fatal("dry-run mutated a price")
	}
	if _, err := b.svc.Confirm(ctx, b.owner, j.ID, bulkimport.ConfirmInput{PreviewHash: "other", ExpectedVersion: j.Version}); code(err) != "preview_stale" {
		t.Fatalf("stale hash: %v", err)
	}
	if _, err := b.svc.Confirm(ctx, uuid.NewString(), j.ID, bulkimport.ConfirmInput{PreviewHash: j.PreviewHash, ExpectedVersion: j.Version}); code(err) != "not_found" {
		t.Fatalf("stranger confirmed: %v", err)
	}
	confirmed, err := b.svc.Confirm(ctx, b.owner, j.ID, bulkimport.ConfirmInput{PreviewHash: j.PreviewHash, ExpectedVersion: j.Version})
	if err != nil || confirmed.Status != bulkimport.StatusConfirmed {
		t.Fatalf("confirm %+v %v", confirmed, err)
	}
	if again, err := b.svc.Confirm(ctx, b.owner, j.ID, bulkimport.ConfirmInput{PreviewHash: j.PreviewHash, ExpectedVersion: j.Version}); err != nil || again.ID != j.ID {
		t.Fatalf("confirm replay %v", err)
	}
	b.drain(t)
	done, err := b.svc.Job(ctx, b.owner, j.ID)
	if err != nil || done.Status != bulkimport.StatusCompleted || done.Outcome != "partial" || done.Summary.Succeeded != 2 || done.Summary.Invalid != 5 {
		t.Fatalf("done %+v %v", done, err)
	}
	checkTotals(t, done)
	v, _ := repository.NewProductVariantRepository(b.pool).FindByID(ctx, red.ID)
	p, _ := b.repo.FindByID(ctx, plain.ID)
	if v.PriceAmount != 150 || p.PriceAmount != 99 || p.Status != domain.StatusDraft {
		t.Fatalf("applied %d %d %s", v.PriceAmount, p.PriceAmount, p.Status)
	}
	// Exactly one audited price change, tagged with the row's operation ref.
	var audits int
	if err = b.pool.QueryRow(ctx, `SELECT count(*) FROM variant_price_audit WHERE variant_id=$1 AND request_id=$2`, red.ID, rows[1].OperationRef).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit rows %d %v", audits, err)
	}
	d, err := b.svc.ReportDownload(ctx, b.owner, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	report := b.files.get(strings.TrimPrefix(d.URL, "memory://"))
	if !strings.Contains(report, "error_code") || !strings.Contains(report, "target_not_found") || !strings.Contains(report, "'=1+1") || strings.Contains(report, ",=1+1") {
		t.Fatalf("report %q", report)
	}
	if _, err = b.svc.Cancel(ctx, b.owner, j.ID, ""); code(err) != "state_conflict" {
		t.Fatalf("cancel finished job: %v", err)
	}
}

func TestBulkPriceStaleRowsCancelAndPermissionRevoked(t *testing.T) {
	b := newBulkFixture(t)
	ctx := t.Context()
	p := b.product(t)
	var skus []*domain.ProductVariant
	for range 4 {
		skus = append(skus, pricedVariant(t, b.fixture, p, 100))
	}
	csv := "sku,price_amount\n"
	for i, v := range skus {
		csv += fmt.Sprintf("%s,%d\n", v.SKU, 200+i)
	}
	j := b.upload(t, "prices", csv)
	// Another writer changes SKU 0 after the preview: no last-write-wins.
	if _, err := b.uc.UpdateVariantPrice(ctx, b.owner, p.ID, skus[0].ID, 111, skus[0].PriceVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := b.svc.Confirm(ctx, b.owner, j.ID, bulkimport.ConfirmInput{PreviewHash: j.PreviewHash, ExpectedVersion: j.Version}); err != nil {
		t.Fatal(err)
	}
	if more, err := b.svc.ApplyNext(ctx); err != nil || !more {
		t.Fatal(err)
	}
	if more, err := b.svc.ApplyNext(ctx); err != nil || !more {
		t.Fatal(err)
	}
	rows := b.rows(t, j.ID)
	if rows[1].Status != "failed" || rows[1].Error.Code != "preview_stale" || rows[2].Status != "succeeded" {
		t.Fatalf("rows %+v %+v", rows[1], rows[2])
	}
	b.vendor.mu.Lock()
	b.vendor.revoked = true
	b.vendor.mu.Unlock()
	b.drain(t)
	stopped, err := repository.ImportStore(b.pool).Job(ctx, j.ID)
	if err != nil || stopped.Status != bulkimport.StatusCompleted || stopped.ErrorCode != "permission_revoked" {
		t.Fatalf("stopped %+v %v", stopped, err)
	}
	if stopped.Summary.Succeeded != 1 || stopped.Summary.Failed != 2 || stopped.Summary.Cancelled != 1 {
		t.Fatalf("summary %+v", stopped.Summary)
	}
	checkTotals(t, stopped)
	if v, _ := repository.NewProductVariantRepository(b.pool).FindByID(ctx, skus[1].ID); v.PriceAmount != 201 {
		t.Fatal("applied row was undone")
	}

	// Cancel a running job: applied rows stay, the rest are cancelled.
	b.vendor.mu.Lock()
	b.vendor.revoked = false
	b.vendor.mu.Unlock()
	j2 := b.upload(t, "prices", fmt.Sprintf("sku,price_amount\n%s,301\n%s,302\n", skus[2].SKU, skus[3].SKU))
	if _, err = b.svc.Confirm(ctx, b.owner, j2.ID, bulkimport.ConfirmInput{PreviewHash: j2.PreviewHash, ExpectedVersion: j2.Version}); err != nil {
		t.Fatal(err)
	}
	if _, err = b.svc.ApplyNext(ctx); err != nil {
		t.Fatal(err)
	}
	cancelled, err := b.svc.Cancel(ctx, b.owner, j2.ID, "wrong file")
	if err != nil || cancelled.Status != bulkimport.StatusCancelled || cancelled.Summary.Succeeded != 1 || cancelled.Summary.Cancelled != 1 || cancelled.Outcome != "partial" {
		t.Fatalf("cancel %+v %v", cancelled, err)
	}
	b.drain(t)
}

func TestBulkRowLeaseIsAppliedOnce(t *testing.T) {
	b := newBulkFixture(t)
	ctx := t.Context()
	p := b.product(t)
	v := pricedVariant(t, b.fixture, p, 100)
	j := b.upload(t, "prices", fmt.Sprintf("sku,price_amount\n%s,250\n", v.SKU))
	if _, err := b.svc.Confirm(ctx, b.owner, j.ID, bulkimport.ConfirmInput{PreviewHash: j.PreviewHash, ExpectedVersion: j.Version}); err != nil {
		t.Fatal(err)
	}
	// A worker claims the row and dies before applying it.
	if _, err := b.pool.Exec(ctx, `UPDATE import_rows SET status='applying',attempts=1,lease_until=now()-interval '1 second' WHERE job_id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			if _, err := b.svc.ApplyNext(ctx); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	b.drain(t)
	var audits int
	if err := b.pool.QueryRow(ctx, `SELECT count(*) FROM variant_price_audit WHERE variant_id=$1 AND new_price=250`, v.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("applied %d times %v", audits, err)
	}
	if _, err := b.pool.Exec(ctx, `UPDATE import_rows SET status='valid' WHERE job_id=$1`, j.ID); err == nil {
		t.Fatal("applied row can be reopened")
	}
}

func TestBulkProductImportIsIdempotentPerClientRow(t *testing.T) {
	b := newBulkFixture(t)
	ctx := t.Context()
	csv := "client_row_id,category_id,name,description,price_amount\n" +
		fmt.Sprintf("SP-1,%s,Áo thun cotton,\"Dòng 1\nDòng 2\n\nĐoạn <3 hai\",199000\n", b.category) +
		fmt.Sprintf("SP-2,%s,,mô tả,1000\n", b.category) +
		fmt.Sprintf("SP-3,%s,Quần,mô tả,1000\n", uuid.NewString()) +
		fmt.Sprintf("SP-1,%s,Trùng,mô tả,1000\n", b.category)
	j := b.upload(t, "products", csv)
	rows := b.rows(t, j.ID)
	if rows[1].Status != "invalid" || rows[1].Error.Code != "duplicate_target" || rows[2].Error.Field != "name" || rows[3].Error.Code != "invalid_category" {
		t.Fatalf("rows %+v", rows)
	}
	j = b.upload(t, "products", "client_row_id,category_id,name,description,price_amount\n"+fmt.Sprintf("SP-1,%s,Áo thun cotton,\"Dòng 1\nDòng 2\n\nĐoạn <3 hai\",199000\n", b.category))
	if _, err := b.svc.Confirm(ctx, b.owner, j.ID, bulkimport.ConfirmInput{PreviewHash: j.PreviewHash, ExpectedVersion: j.Version}); err != nil {
		t.Fatal(err)
	}
	b.drain(t)
	row := b.rows(t, j.ID)[1]
	if row.Status != "succeeded" || !strings.HasPrefix(row.ResultRef, "product:") {
		t.Fatalf("row %+v", row)
	}
	created, err := b.repo.FindByID(ctx, strings.TrimPrefix(row.ResultRef, "product:"))
	if err != nil || created.Status != domain.StatusDraft || created.Description != "<p>Dòng 1<br>Dòng 2</p><p>Đoạn &lt;3 hai</p>" {
		t.Fatalf("created %+v %v", created, err)
	}
	// The same file again: the client_row_id is already imported, and the
	// earlier job that applied this file is pointed out before confirm.
	again := b.upload(t, "products", "client_row_id,category_id,name,description,price_amount\n"+fmt.Sprintf("SP-1,%s,Áo thun cotton,\"Dòng 1\nDòng 2\n\nĐoạn <3 hai\",199000\n", b.category))
	if again.DuplicateOf != j.ID || b.rows(t, again.ID)[1].Error.Code != "already_imported" {
		t.Fatalf("duplicate %+v", again)
	}
	var products int
	if err = b.pool.QueryRow(ctx, `SELECT count(*) FROM products WHERE vendor_id=$1 AND name='Áo thun cotton'`, b.shop).Scan(&products); err != nil || products != 1 {
		t.Fatalf("products %d %v", products, err)
	}
}

func TestBulkDuplicateFileNeedsAcknowledgementAndExport(t *testing.T) {
	b := newBulkFixture(t)
	ctx := t.Context()
	p := b.product(t)
	v := pricedVariant(t, b.fixture, p, 100)
	csv := fmt.Sprintf("sku,price_amount\n%s,170\n", v.SKU)
	first := b.upload(t, "prices", csv)
	second := b.upload(t, "prices", csv)
	if _, err := b.svc.Confirm(ctx, b.owner, first.ID, bulkimport.ConfirmInput{PreviewHash: first.PreviewHash, ExpectedVersion: first.Version}); err != nil {
		t.Fatal(err)
	}
	in := bulkimport.ConfirmInput{PreviewHash: second.PreviewHash, ExpectedVersion: second.Version}
	if _, err := b.svc.Confirm(ctx, b.owner, second.ID, in); code(err) != "duplicate_file" {
		t.Fatalf("duplicate file confirmed silently: %v", err)
	}
	in.AcknowledgeDuplicate = true
	if _, err := b.svc.Confirm(ctx, b.owner, second.ID, in); err != nil {
		t.Fatal(err)
	}
	b.drain(t)
	// The second job hits the version the first one changed.
	if r := b.rows(t, second.ID)[1]; r.Status != "failed" || r.Error.Code != "preview_stale" {
		t.Fatalf("second %+v", r)
	}
	e, err := b.svc.CreateExport(ctx, b.owner, b.shop, "prices", "export-key-0001")
	if err != nil {
		t.Fatal(err)
	}
	if more, err := b.svc.ExportNext(ctx); err != nil || !more {
		t.Fatal(err)
	}
	ready, err := b.svc.Export(ctx, b.owner, e.ID)
	if err != nil || ready.Status != "ready" || ready.RowCount != 1 {
		t.Fatalf("export %+v %v", ready, err)
	}
	d, err := b.svc.ExportDownload(ctx, b.owner, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	body := b.files.get(strings.TrimPrefix(d.URL, "memory://"))
	if !strings.HasPrefix(body, "\xEF\xBB\xBFsku,product_id,price_amount,expected_version,product_name\n") || !strings.Contains(body, v.SKU+","+p.ID+",170,2,") {
		t.Fatalf("export %q", body)
	}
	if _, err = b.svc.Export(ctx, uuid.NewString(), e.ID); code(err) != "not_found" {
		t.Fatalf("stranger read export: %v", err)
	}
}

func TestBulkMigrationRollbackKeepsAppliedImports(t *testing.T) {
	pool := catalogDB(t)
	ctx := t.Context()
	up, err := os.ReadFile("../../migrations/000018_bulk_operations.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../migrations/000018_bulk_operations.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("unused rollback: %v", err)
	}
	if _, err = pool.Exec(ctx, string(up)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO import_jobs(vendor_id,kind,template_version,requested_by,idempotency_key,file_name,file_hash,file_bytes,object_key,confirmed_at)
 VALUES(gen_random_uuid(),'prices',1,gen_random_uuid(),'k','f.csv','h',1,'o',now())`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(down)); err == nil {
		t.Fatal("rollback dropped applied import history")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM import_job_events`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO import_job_events(job_id,vendor_id,action) VALUES(gen_random_uuid(),gen_random_uuid(),'uploaded')`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE import_job_events SET action='x'`); err == nil {
		t.Fatal("import events are editable")
	}
}
