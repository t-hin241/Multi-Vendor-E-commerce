package repository_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/repository"
	"shopee/backend/services/shipment/internal/usecase"
)

type memoryStore struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (m *memoryStore) Put(_ context.Context, key string, data []byte, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = data
	return nil
}

func (m *memoryStore) Open(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.objects[key]
	if !ok {
		return nil, errors.New("no such object")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *memoryStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

func testImage(t *testing.T, format string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})
	var out bytes.Buffer
	var err error
	if format == "png" {
		err = png.Encode(&out, img)
	} else {
		err = jpeg.Encode(&out, img, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// PW-038: Shipment keeps the evidence of its failure reports. With storage
// configured, lost needs the carrier's confirmation; a report attaches
// only its reporter's uploads for that shipment, once; uploads no report
// used are removed after a day.
func TestLostReportCarriesTheCarriersEvidence(t *testing.T) {
	e := newEnv(t, false)
	ctx := t.Context()
	store := &memoryStore{objects: map[string][]byte{}}
	e.uc.DeliveryResolution = true
	e.uc.Evidence, e.uc.EvidenceRepo = store, repository.EvidenceRepository{Pool: e.pool}
	vendor := usecase.Actor{ID: e.userA, Role: domain.ActorVendor}
	stranger := usecase.Actor{ID: uuid.NewString(), Role: domain.ActorVendor}
	admin := usecase.Actor{ID: uuid.NewString(), Role: domain.ActorAdmin}

	s := e.paidShipment(t)
	if _, err := e.uc.MarkShipped(ctx, vendor, s.ID, "TRACK-EV-1"); err != nil {
		t.Fatal(err)
	}
	current, _ := e.uc.AdminGet(ctx, admin.ID, s.ID)
	_, err := e.uc.ReportFailure(ctx, admin, s.ID, usecase.FailureReport{Kind: "lost", Reason: "Hãng xác nhận thất lạc", ExpectedVersion: current.Version})
	expectCode(t, err, "evidence_required")

	if _, err := e.uc.UploadEvidence(ctx, stranger, s.ID, testImage(t, "png")); err == nil {
		t.Fatal("only the shop of the shipment uploads for it")
	}
	_, err = e.uc.UploadEvidence(ctx, admin, s.ID, []byte("%PDF-1.4 not a photo"))
	expectCode(t, err, "unsupported_evidence")
	shopPhoto, err := e.uc.UploadEvidence(ctx, vendor, s.ID, testImage(t, "png"))
	if err != nil || shopPhoto.ContentType != "image/png" || shopPhoto.State != domain.EvidenceUploaded {
		t.Fatalf("shop upload: %+v %v", shopPhoto, err)
	}
	confirmation, err := e.uc.UploadEvidence(ctx, admin, s.ID, testImage(t, "jpeg"))
	if err != nil || confirmation.ContentType != "image/jpeg" {
		t.Fatalf("admin upload: %+v %v", confirmation, err)
	}

	// An admin cannot cite the shop's upload; the report is refused whole.
	_, err = e.uc.ReportFailure(ctx, admin, s.ID, usecase.FailureReport{Kind: "lost", Reason: "Hãng xác nhận thất lạc",
		ExpectedVersion: current.Version, EvidenceIDs: []string{confirmation.ID, shopPhoto.ID}})
	expectCode(t, err, "evidence_unavailable")
	if again, _ := e.uc.AdminGet(ctx, admin.ID, s.ID); again.Status != domain.StatusShipped {
		t.Fatal("a refused report changes nothing")
	}
	lost, err := e.uc.ReportFailure(ctx, admin, s.ID, usecase.FailureReport{Kind: "lost", Reason: "Hãng xác nhận thất lạc",
		ExpectedVersion: current.Version, EvidenceIDs: []string{confirmation.ID, confirmation.ID}})
	if err != nil || lost.Status != domain.StatusLost {
		t.Fatalf("lost with evidence: %+v %v", lost, err)
	}

	// The shop sees the attached confirmation and its own upload; a stranger nothing.
	items, err := e.uc.ListEvidence(ctx, vendor, s.ID)
	if err != nil || len(items) != 2 {
		t.Fatalf("shop list: %d %v", len(items), err)
	}
	if _, err := e.uc.ListEvidence(ctx, stranger, s.ID); err == nil {
		t.Fatal("a stranger cannot read the evidence")
	}
	file, err := e.uc.OpenEvidence(ctx, vendor, s.ID, confirmation.ID)
	if err != nil || file.ContentType != "image/jpeg" {
		t.Fatalf("open: %v", err)
	}
	_ = file.Body.Close()
	adminItems, _ := e.uc.ListEvidence(ctx, admin, s.ID)
	if len(adminItems) != 1 || adminItems[0].ID != confirmation.ID || *adminItems[0].ReportKind != "lost" {
		t.Fatalf("admins see the attached evidence only: %+v", adminItems)
	}

	// An attached file is never reused by another report.
	other := e.paidShipment(t)
	if _, err := e.uc.MarkShipped(ctx, vendor, other.ID, "TRACK-EV-2"); err != nil {
		t.Fatal(err)
	}
	o, _ := e.uc.AdminGet(ctx, admin.ID, other.ID)
	_, err = e.uc.ReportFailure(ctx, admin, other.ID, usecase.FailureReport{Kind: "lost", Reason: "Thất lạc",
		ExpectedVersion: o.Version, EvidenceIDs: []string{confirmation.ID}})
	var app *apperror.Error
	if !errors.As(err, &app) || app.Code != "evidence_unavailable" {
		t.Fatalf("reused evidence: %v", err)
	}

	// The shop's upload no report used goes after a day; the confirmation stays.
	e.uc.Now = func() time.Time { return time.Now().Add(usecase.EvidenceOrphanAge + time.Hour) }
	if n, err := e.uc.CleanEvidence(ctx); err != nil || n < 1 {
		t.Fatalf("cleanup: %d %v", n, err)
	}
	if e.count(t, `SELECT count(*) FROM shipment_evidence WHERE id = $1 AND state = 'deleted'`, shopPhoto.ID) != 1 {
		t.Fatal("the orphan is tombstoned")
	}
	if _, ok := store.objects[shopPhoto.ObjectKey]; ok {
		t.Fatal("the orphan's file is removed")
	}
	if _, ok := store.objects[confirmation.ObjectKey]; !ok {
		t.Fatal("attached evidence stays")
	}
}
