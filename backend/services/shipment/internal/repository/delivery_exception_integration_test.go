package repository_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/repository"
	"shopee/backend/services/shipment/internal/usecase"
)

// Commit another request at a chosen boundary, without depending on the
// scheduler to expose the gap between the operation lookup and the insert.
type replacementCommitRace struct {
	usecase.ShipmentRepositoryPort
	afterMiss    func()
	beforeCreate func()
	createErr    error
}

func (r *replacementCommitRace) FindByReplacementOperation(ctx context.Context, id string) (*domain.Shipment, error) {
	s, err := r.ShipmentRepositoryPort.FindByReplacementOperation(ctx, id)
	if errors.Is(err, repository.ErrShipmentNotFound) && r.afterMiss != nil {
		commit := r.afterMiss
		r.afterMiss = nil
		commit()
	}
	return s, err
}

func (r *replacementCommitRace) CreateReplacement(ctx context.Context, s *domain.Shipment, id string) error {
	if r.beforeCreate != nil {
		commit := r.beforeCreate
		r.beforeCreate = nil
		commit()
		return r.createErr
	}
	return r.ShipmentRepositoryPort.CreateReplacement(ctx, s, id)
}

func TestReplacementReplayAfterConcurrentCommit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		createErr error
	}{
		{name: "before_latest_attempt_read"},
		{name: "active_attempt_constraint", createErr: repository.ErrActiveAttemptExists},
		{name: "operation_constraint", createErr: repository.ErrShipmentAlreadyExists},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, false)
			ctx := t.Context()
			e.uc.DeliveryResolution = true
			s := e.paidShipment(t)
			vendor := usecase.Actor{ID: e.userA, Role: domain.ActorVendor}
			admin := usecase.Actor{ID: uuid.NewString(), Role: domain.ActorAdmin}
			if _, err := e.uc.MarkShipped(ctx, vendor, s.ID, "TRACK-REPLAY-RACE"); err != nil {
				t.Fatal(err)
			}
			if _, err := e.uc.MarkReturned(ctx, admin, s.ID, "fake returned package"); err != nil {
				t.Fatal(err)
			}
			in := usecase.ReplacementAttempt{OperationID: "delivery_exception:" + uuid.NewString(), VendorOrderID: s.VendorOrderID,
				OriginalShipmentID: s.ID, AttemptNo: 2, EligibilityRef: uuid.NewString(),
				Destination: usecase.Destination{RecipientName: "Test recipient", Phone: "0000000000", Province: "HN", District: "D2", Ward: "W2", StreetAddress: "Test address"}}
			winner := *e.uc
			var committed *domain.Shipment
			commit := func() {
				var err error
				committed, err = winner.CreateReplacementAttempt(ctx, in)
				if err != nil {
					t.Fatal(err)
				}
			}
			race := &replacementCommitRace{ShipmentRepositoryPort: e.uc.Shipments, createErr: tc.createErr}
			if tc.createErr == nil {
				race.afterMiss = commit
			} else {
				race.beforeCreate = commit
			}
			e.uc.Shipments = race
			replayed, err := e.uc.CreateReplacementAttempt(ctx, in)
			if err != nil || replayed == nil || replayed.ID != committed.ID {
				t.Fatalf("committed operation must replay: %+v %v", replayed, err)
			}
			if e.count(t, `SELECT count(*) FROM shipments WHERE replacement_operation_id=$1`, in.OperationID) != 1 ||
				e.count(t, `SELECT count(*) FROM shipment_tracking_events WHERE shipment_id=$1`, committed.ID) != 1 {
				t.Fatal("replay duplicated the shipment or its tracking event")
			}
			changed := in
			changed.OriginalShipmentID = uuid.NewString()
			var app *apperror.Error
			if _, err := e.uc.CreateReplacementAttempt(ctx, changed); !errors.As(err, &app) || app.Code != apperror.CodeConflict {
				t.Fatalf("operation reused for another original shipment: %v", err)
			}
		})
	}
}

// AF-04: the attempt limit, a returned package and a lost one each tell
// Order once (with the flag); only an admin declares a package lost; a
// failure report is checked against the version the caller read.
func TestDeliveryFailureFactsReachOrderOnce(t *testing.T) {
	e := newEnv(t, false)
	ctx := t.Context()
	vendor := usecase.Actor{ID: e.userA, Role: domain.ActorVendor}
	admin := usecase.Actor{ID: uuid.NewString(), Role: domain.ActorAdmin}

	// Flag off: the status facts alone, as before.
	off := e.paidShipment(t)
	if _, err := e.uc.MarkShipped(ctx, vendor, off.ID, "TRACK-DX-0"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.uc.ReportFailure(ctx, admin, off.ID, usecase.FailureReport{Kind: "lost", Reason: "x", ExpectedVersion: 1}); err == nil {
		t.Fatal("failure reports need the flag")
	}
	if _, err := e.uc.MarkReturned(ctx, admin, off.ID, "came back"); err != nil {
		t.Fatal(err)
	}
	if e.count(t, `SELECT count(*) FROM shipment_outbox WHERE shipment_id = $1 AND event_type LIKE 'exception_%'`, off.ID) != 0 {
		t.Fatal("no exception facts with the flag off")
	}

	e.uc.DeliveryResolution, e.uc.AttemptLimit = true, 2
	s := e.paidShipment(t)
	shipped, err := e.uc.MarkShipped(ctx, vendor, s.ID, "TRACK-DX-1")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := e.uc.RecordFailedAttempt(ctx, vendor, s.ID, "Không có người nhận"); err != nil {
			t.Fatal(err)
		}
	}
	if e.count(t, `SELECT count(*) FROM shipment_outbox WHERE shipment_id = $1 AND event_type = 'exception_attempts_exhausted' AND failed_attempts = 2`, s.ID) != 1 {
		t.Fatal("the attempt limit tells Order once")
	}
	current, _ := e.uc.AdminGet(ctx, admin.ID, s.ID)
	if _, err := e.uc.ReportFailure(ctx, vendor, s.ID, usecase.FailureReport{Kind: "lost", Reason: "Mất hàng", ExpectedVersion: current.Version}); err == nil {
		t.Fatal("a shop cannot declare a package lost")
	}
	if _, err := e.uc.ReportFailure(ctx, vendor, s.ID, usecase.FailureReport{Kind: "returned", Reason: "Hàng về", ExpectedVersion: shipped.Version}); err == nil {
		t.Fatal("a report on a stale version is refused")
	}
	var wg sync.WaitGroup
	var ok atomic.Int32
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.uc.ReportFailure(t.Context(), vendor, s.ID, usecase.FailureReport{Kind: "returned", Reason: "Hàng về kho", ExpectedVersion: current.Version}); err == nil {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() < 1 || e.count(t, `SELECT count(*) FROM shipment_outbox WHERE shipment_id = $1 AND event_type = 'exception_returned'`, s.ID) != 1 {
		t.Fatalf("returned reported once (%d accepted)", ok.Load())
	}

	lost := e.paidShipment(t)
	if _, err := e.uc.MarkShipped(ctx, vendor, lost.ID, "TRACK-DX-2"); err != nil {
		t.Fatal(err)
	}
	l, _ := e.uc.AdminGet(ctx, admin.ID, lost.ID)
	l, err = e.uc.ReportFailure(ctx, admin, lost.ID, usecase.FailureReport{Kind: "lost", Reason: "Hãng xác nhận thất lạc", ExpectedVersion: l.Version})
	if err != nil || l.Status != domain.StatusLost || l.LostAt == nil {
		t.Fatalf("lost: %+v %v", l, err)
	}
	if e.count(t, `SELECT count(*) FROM shipment_outbox WHERE shipment_id = $1 AND event_type = 'exception_lost'`, lost.ID) != 1 ||
		e.count(t, `SELECT count(*) FROM shipment_outbox WHERE shipment_id = $1 AND event_type = 'returned'`, lost.ID) != 0 {
		t.Fatal("lost is an exception fact only (old consumers never see a new status)")
	}

	// Packages returned before the feature get their fact once.
	outbox := repository.OrderOutbox{Pool: e.pool}
	n, err := outbox.EnqueueLegacyReturned(ctx, 100)
	if err != nil || n != 1 {
		t.Fatalf("legacy returned backfilled once: %d %v", n, err)
	}
	if n, _ := outbox.EnqueueLegacyReturned(ctx, 100); n != 0 {
		t.Fatal("the backfill does not repeat")
	}
}

// AF-04: a redelivery is a new attempt after a returned or lost one, once
// per Order operation; never while another attempt is active, never from
// an older attempt, and the final attempt is never reset.
func TestReplacementAttemptOncePerOperation(t *testing.T) {
	e := newEnv(t, false)
	ctx := t.Context()
	e.uc.DeliveryResolution = true
	vendor := usecase.Actor{ID: e.userA, Role: domain.ActorVendor}
	admin := usecase.Actor{ID: uuid.NewString(), Role: domain.ActorAdmin}
	s := e.paidShipment(t)
	in := usecase.ReplacementAttempt{OperationID: "delivery_exception:" + uuid.NewString() + ":redelivery:1", VendorOrderID: s.VendorOrderID,
		OriginalShipmentID: s.ID, AttemptNo: 2, EligibilityRef: uuid.NewString(),
		Destination: usecase.Destination{RecipientName: "Người nhận", Phone: "0911111111", Province: "HN", District: "D2", Ward: "W2", StreetAddress: "2 New St"}}
	if _, err := e.uc.CreateReplacementAttempt(ctx, in); err == nil {
		t.Fatal("no redelivery while the first attempt is active")
	}
	if _, err := e.uc.MarkShipped(ctx, vendor, s.ID, "TRACK-RA-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.uc.MarkReturned(ctx, admin, s.ID, "Hàng hoàn về"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	ids := make([]string, 4)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r, err := e.uc.CreateReplacementAttempt(t.Context(), in); err == nil {
				ids[i] = r.ID
			} else {
				t.Errorf("retry of one operation: %v", err)
			}
		}()
	}
	wg.Wait()
	for _, id := range ids[1:] {
		if id != ids[0] {
			t.Fatalf("one attempt per operation: %v", ids)
		}
	}
	r, err := e.uc.AdminGet(ctx, admin.ID, ids[0])
	if err != nil || r.AttemptNo != 2 || r.OriginalShipmentID == nil || *r.OriginalShipmentID != s.ID || r.Status != domain.StatusPending ||
		*r.StreetAddress != "2 New St" || r.CarrierID == nil {
		t.Fatalf("attempt 2 to the agreed address: %+v %v", r, err)
	}
	if e.count(t, `SELECT count(*) FROM shipments WHERE id = $1 AND status = 'returned'`, s.ID) != 1 {
		t.Fatal("the returned attempt is never reset")
	}
	other := in
	other.OperationID = "delivery_exception:" + uuid.NewString() + ":redelivery:2"
	other.AttemptNo = 3
	other.OriginalShipmentID = r.ID
	if _, err := e.uc.CreateReplacementAttempt(ctx, other); err == nil {
		t.Fatal("no third attempt while the second is active")
	}
	stale := in
	stale.OperationID = "delivery_exception:" + uuid.NewString() + ":redelivery:3"
	if _, err := e.uc.CreateReplacementAttempt(ctx, stale); err == nil {
		t.Fatal("an older attempt cannot be redelivered again")
	}
	if latest, err := repository.NewShipmentRepository(e.pool).FindByVendorOrderID(ctx, s.VendorOrderID); err != nil || latest.ID != r.ID {
		t.Fatalf("the vendor order's shipment is its latest attempt: %+v %v", latest, err)
	}
	// The redelivery claims the handover and ships like any package.
	if _, err := e.uc.MarkShipped(ctx, vendor, r.ID, "TRACK-RA-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE shipments SET attempt_no = 1 WHERE id = $1`, r.ID); err == nil {
		t.Fatal("two attempts cannot share a number")
	}
}
