package repository_test

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/repository"
	"shopee/backend/services/shipment/internal/usecase"
)

// AF-05: one active parcel per return, once per Order operation; a
// destination corrected before dispatch moves the parcel, never after; a
// dispatch and a receipt are idempotent; the flag stops new parcels only.
func TestReturnShipmentFollowsOrdersCommands(t *testing.T) {
	pool := shipmentDB(t)
	ctx := t.Context()
	uc := &usecase.ReturnShipmentUseCase{Tx: repository.Transactions{Pool: pool}, Returns: repository.ReturnShipmentRepository{Pool: pool},
		Identity: allowAdmin{}, Log: zerolog.Nop()}
	returnID := uuid.NewString()
	in := usecase.ReturnAuthorization{OperationID: "return:" + returnID + ":auth:1", ReturnID: returnID, OrderID: uuid.NewString(),
		VendorID: uuid.NewString(), BuyerID: uuid.NewString(), AuthorizationVersion: 1, ReceivingHours: "8-17h",
		Destination: usecase.Destination{RecipientName: "Kho", Phone: "0900000001", Province: "HN", District: "D", Ward: "W", StreetAddress: "1 Kho"}}
	if _, err := uc.Authorize(ctx, in); err == nil {
		t.Fatal("no new parcel with the flag off")
	}
	uc.Enabled = true
	var wg sync.WaitGroup
	ids := make([]string, 4)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s, err := uc.Authorize(t.Context(), in); err == nil {
				ids[i] = s.ID
			} else {
				t.Errorf("authorize: %v", err)
			}
		}()
	}
	wg.Wait()
	for _, id := range ids[1:] {
		if id != ids[0] {
			t.Fatalf("one parcel per return: %v", ids)
		}
	}
	v2 := in
	v2.OperationID, v2.AuthorizationVersion, v2.Destination.Province = "return:"+returnID+":auth:2", 2, "HCM"
	moved, err := uc.Authorize(ctx, v2)
	if err != nil || moved.ID != ids[0] || moved.Province != "HCM" || moved.AuthorizationVersion != 2 {
		t.Fatalf("a destination corrected before dispatch moves the parcel: %+v %v", moved, err)
	}
	if again, err := uc.Authorize(ctx, in); err != nil || again.Province != "HCM" {
		t.Fatalf("a late repeat of the old authorization changes nothing: %+v %v", again, err)
	}
	uc.Enabled = false
	dispatch := usecase.ReturnDispatch{OperationID: "return_dispatch:" + returnID, CarrierName: "GHN", TrackingNumber: "VN0001", DispatchedAt: time.Now()}
	sent, err := uc.Dispatch(ctx, moved.ID, dispatch)
	if err != nil || sent.Status != domain.ReturnInTransit {
		t.Fatalf("a parcel already open keeps moving with the flag off: %+v %v", sent, err)
	}
	if again, err := uc.Dispatch(ctx, moved.ID, dispatch); err != nil || again.Version != sent.Version {
		t.Fatalf("a repeated dispatch answers the same: %+v %v", again, err)
	}
	v3 := v2
	v3.OperationID, v3.AuthorizationVersion = "return:"+returnID+":auth:3", 3
	if _, err := uc.Authorize(ctx, v3); err == nil {
		t.Fatal("no new destination after dispatch")
	}
	got, err := uc.Receive(ctx, moved.ID)
	if err != nil || got.Status != domain.ReturnReceived || got.ReceivedAt == nil {
		t.Fatalf("received: %+v %v", got, err)
	}
	if _, err := uc.Receive(ctx, moved.ID); err != nil {
		t.Fatalf("a repeated receipt is accepted: %v", err)
	}
	if _, err := uc.Exception(ctx, moved.ID, "lost"); err == nil {
		t.Fatal("a received parcel cannot become an exception")
	}
	list, err := uc.AdminList(ctx, uuid.NewString(), "received", 10, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("admin list: %v %v", list, err)
	}
}
