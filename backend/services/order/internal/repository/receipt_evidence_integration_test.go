package repository_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
	"shopee/backend/services/order/internal/usecase"
)

type memoryObjects struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (m *memoryObjects) Put(_ context.Context, key string, data []byte, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = data
	return nil
}

func (m *memoryObjects) Open(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return io.NopCloser(bytes.NewReader(m.objects[key])), nil
}

func (m *memoryObjects) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

func fakePNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// PW-038: a receipt carries the recorder's evidence images, attached in
// the same transaction; someone else's or a used image refuses the whole
// step, and only the shop and admins can open them.
func TestReceiptEvidenceIsAttachedWithTheStep(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	f := newReturnFixture(t, pool)
	f.dests.set(destination(1, "79"))
	f.uc.Attachments = &memoryObjects{objects: map[string][]byte{}}
	f.uc.ReceiptEvidence = repository.NewSupportCaseRepository(pool)
	rr := f.approvedReturn(t)
	runDue(t, pool, f.uc)
	shop := usecase.SupportActor{ID: f.vendorUser, Role: "vendor"}
	mine, err := f.uc.UploadSupportAttachment(ctx, shop, fakePNG(t))
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := f.uc.UploadSupportAttachment(ctx, usecase.SupportActor{ID: uuid.NewString(), Role: "vendor"}, fakePNG(t))
	if err != nil {
		t.Fatal(err)
	}
	receipt := func(ids ...string) (*domain.ReturnRequest, error) {
		return f.uc.RecordReturnReceipt(ctx, shop, rr.ID, usecase.ReturnReceiptInput{Sellable: 1, Damaged: 1, Note: "Một chiếc móp",
			ExpectedVersion: f.load(t, rr.ID).Version, EvidenceIDs: ids})
	}
	if _, err := receipt(theirs.ID); code(err) != usecase.CodeEvidenceUnavailable {
		t.Fatalf("another person's image is refused: %v", err)
	}
	if n := countRowsIn(t, pool, `SELECT count(*) FROM return_goods_receipts WHERE return_id = $1`, rr.ID); n != 0 {
		t.Fatal("the refused step saved nothing")
	}
	if _, err := receipt(mine.ID); err != nil {
		t.Fatal(err)
	}
	items, err := f.uc.ListReceiptEvidence(ctx, shop, usecase.EvidenceReturn, rr.ID)
	if err != nil || len(items) != 1 || items[0].ID != mine.ID {
		t.Fatalf("the shop sees its evidence: %+v %v", items, err)
	}
	file, err := f.uc.OpenReceiptEvidence(ctx, usecase.SupportActor{ID: f.admin, Role: "admin"}, usecase.EvidenceReturn, rr.ID, mine.ID)
	if err != nil || file.ContentType != "image/png" {
		t.Fatalf("an admin opens it: %v", err)
	}
	file.Body.Close()
	if _, err := f.uc.ListReceiptEvidence(ctx, usecase.SupportActor{ID: uuid.NewString(), Role: "vendor"}, usecase.EvidenceReturn, rr.ID); err == nil {
		t.Fatal("another shop cannot list it")
	}
	got := f.load(t, rr.ID)
	if _, err := f.uc.CorrectReturnReceipt(ctx, f.admin, rr.ID, usecase.ReturnReceiptCorrection{Sellable: 1, Missing: 1, Note: "Sửa",
		ExpectedVersion: got.Version, EvidenceIDs: []string{mine.ID}}); code(err) != usecase.CodeEvidenceUnavailable {
		t.Fatalf("a used image cannot prove another step: %v", err)
	}

	// Receipt photos are kept at most 30 days after the step.
	store := f.uc.Attachments.(*memoryObjects)
	if _, err := f.uc.CleanSupportAttachments(ctx, 100); err != nil || countRowsIn(t, pool, `SELECT count(*) FROM case_attachments WHERE id = $1 AND state = 'attached'`, mine.ID) != 1 {
		t.Fatalf("a fresh receipt photo stays: %v", err)
	}
	f.uc.Now = func() time.Time { return time.Now().Add(usecase.ReceiptEvidenceRetention + time.Hour) }
	if _, err := f.uc.CleanSupportAttachments(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if items, _ := f.uc.ListReceiptEvidence(ctx, shop, usecase.EvidenceReturn, rr.ID); len(items) != 0 {
		t.Fatalf("the photo is removed after 30 days: %+v", items)
	}
	if countRowsIn(t, pool, `SELECT count(*) FROM case_attachments WHERE id = $1 AND state = 'deleted'`, mine.ID) != 1 {
		t.Fatal("the row becomes a tombstone")
	}
	if _, ok := store.objects[mine.ObjectKey]; ok {
		t.Fatal("its object is deleted")
	}
}
