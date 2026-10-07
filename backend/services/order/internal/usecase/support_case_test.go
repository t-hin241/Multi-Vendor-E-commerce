package usecase_test

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/usecase"
)

var (
	buyer   = usecase.SupportActor{ID: "buyer-1", Role: "buyer"}
	vendorA = usecase.SupportActor{ID: "user-a", Role: "vendor"}
	vendorB = usecase.SupportActor{ID: "user-b", Role: "vendor"}
	admin1  = usecase.SupportActor{ID: "admin-1", Role: "admin"}
	admin2  = usecase.SupportActor{ID: "admin-2", Role: "admin"}
)

// openCase opens a not_received case on vendor-a's part of a delivered order.
func openCase(t *testing.T, f *checkoutFixture) (*domain.Order, *domain.VendorOrder, *domain.SupportCase) {
	t.Helper()
	order, vos := deliveredOrder(t, f)
	sc, replayed, err := f.uc.CreateSupportCase(t.Context(), "buyer-1", usecase.CreateSupportCaseInput{
		OrderID: order.ID, VendorOrderID: vos["vendor-a"].ID, Category: "not_received", Message: "Tôi chưa nhận được hàng",
	})
	if err != nil || replayed {
		t.Fatalf("create case: %v (replayed %v)", err, replayed)
	}
	return order, vos["vendor-a"], sc
}

func assign(t *testing.T, f *checkoutFixture, caseID string, admin usecase.SupportActor) *domain.SupportCase {
	t.Helper()
	sc, err := f.uc.AssignSupportCase(t.Context(), admin.ID, caseID, admin.ID, f.support.get(caseID).Version, "Take responsibility")
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	return sc
}

func hasEffect(f *checkoutFixture, targetPrefix string) bool {
	f.effects.mu.Lock()
	defer f.effects.mu.Unlock()
	for _, e := range f.effects.effects {
		if strings.HasPrefix(e.Target, targetPrefix) {
			return true
		}
	}
	return false
}

func TestSupportCase_EndToEnd_OpenReplyRefundConfirmClose(t *testing.T) {
	f := newCheckoutFixture()
	order, vo, sc := openCase(t, f)
	if sc.Status != domain.CaseOpen || sc.DueAt == nil || !sc.FinancialHold || sc.VendorID != "vendor-a" {
		t.Fatalf("unexpected new case %+v", sc)
	}
	if !hasEffect(f, "support_case_opened:"+sc.ID) {
		t.Fatal("the buyer must be told the case was received (through the outbox)")
	}

	if _, _, err := f.uc.PostSupportMessage(t.Context(), vendorA, sc.ID, usecase.SupportMessageInput{Text: "Shop đã giao cho đơn vị vận chuyển"}); err != nil {
		t.Fatalf("the selling shop answers: %v", err)
	}
	sc = assign(t, f, sc.ID, admin1)
	if sc.Status != domain.CaseInProgress || sc.AssigneeID == nil || *sc.AssigneeID != "admin-1" {
		t.Fatalf("assignment must put the case in progress, got %+v", sc)
	}

	refund, err := f.uc.AdminRequestRefund(t.Context(), "admin-1", refundInput(order.ID, vo.ID, "", domain.RefundReasonDispute, vo.Total()))
	if err != nil {
		t.Fatal(err)
	}
	sc, pending, err := f.uc.ResolveSupportCase(t.Context(), "admin-1", sc.ID, usecase.ResolveInput{
		Kind: domain.ResolutionRefund, LinkedOperationID: refund.ID, Reason: "Hoàn tiền vì hàng thất lạc", ExpectedVersion: sc.Version,
	})
	if err != nil || !pending || sc.Status != domain.CaseResolutionPending {
		t.Fatalf("a refund not yet confirmed keeps the case pending, got %v pending=%v %+v", err, pending, sc)
	}
	_, err = f.uc.ConfirmSupportCase(t.Context(), "buyer-1", sc.ID)
	expectCode(t, err, apperror.CodeConflict)

	if err := f.uc.ApplyRefundOutcome(t.Context(), domain.RefundOutcome{RefundID: refund.ID, PaymentRefundID: "pr-1",
		Status: domain.RefundSucceeded, Amount: vo.Total(), Currency: "VND"}); err != nil {
		t.Fatal(err)
	}
	if got := f.support.get(sc.ID); got.Status != domain.CaseResolved || got.ResolvedAt == nil {
		t.Fatalf("a confirmed refund resolves the case, got %s", got.Status)
	}
	if !hasEffect(f, "support_case_resolved:"+sc.ID) {
		t.Fatal("the buyer must be told the case is resolved")
	}
	closed, err := f.uc.ConfirmSupportCase(t.Context(), "buyer-1", sc.ID)
	if err != nil || closed.Status != domain.CaseClosed || closed.ClosedAt == nil {
		t.Fatalf("the buyer's confirmation closes the case: %v %+v", err, closed)
	}
	want := []string{"opened", "assigned", "resolution_proposed", "resolution_confirmed", "confirmed"}
	if got := f.support.actions(sc.ID); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("timeline = %v, want %v", got, want)
	}
	if f.audit.count("support_case_assigned") != 1 || f.audit.count("support_case_resolution_proposed") != 1 {
		t.Fatal("admin steps must be audited")
	}
}

func TestSupportCase_FailedRefundNeverResolvesTheCase(t *testing.T) {
	f := newCheckoutFixture()
	order, vo, sc := openCase(t, f)
	sc = assign(t, f, sc.ID, admin1)
	refund, err := f.uc.AdminRequestRefund(t.Context(), "admin-1", refundInput(order.ID, vo.ID, "", domain.RefundReasonDispute, 1000))
	if err != nil {
		t.Fatal(err)
	}
	sc, _, err = f.uc.ResolveSupportCase(t.Context(), "admin-1", sc.ID, usecase.ResolveInput{Kind: domain.ResolutionRefund,
		LinkedOperationID: refund.ID, Reason: "Hoàn một phần", ExpectedVersion: sc.Version})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.uc.ApplyRefundOutcome(t.Context(), domain.RefundOutcome{RefundID: refund.ID, Status: domain.RefundFailed,
		FailureReason: "bank refused"}); err != nil {
		t.Fatal(err)
	}
	got := f.support.get(sc.ID)
	if got.Status != domain.CaseInProgress || got.ResolutionRef != nil {
		t.Fatalf("a failed refund puts the case back in progress, got %+v", got)
	}
	// The failed refund can no longer be linked.
	_, _, err = f.uc.ResolveSupportCase(t.Context(), "admin-1", sc.ID, usecase.ResolveInput{Kind: domain.ResolutionRefund,
		LinkedOperationID: refund.ID, Reason: "again", ExpectedVersion: got.Version})
	expectCode(t, err, domain.CodeSupportOperationInvalid)
}

func TestSupportCase_LinkedOperationMustBelongToTheCase(t *testing.T) {
	f := newCheckoutFixture()
	order, vos := deliveredOrder(t, f)
	sc, _, err := f.uc.CreateSupportCase(t.Context(), "buyer-1", usecase.CreateSupportCaseInput{OrderID: order.ID,
		VendorOrderID: vos["vendor-a"].ID, Category: "wrong_items", Message: "Sai màu"})
	if err != nil {
		t.Fatal(err)
	}
	sc = assign(t, f, sc.ID, admin1)
	otherShop, err := f.uc.AdminRequestRefund(t.Context(), "admin-1", refundInput(order.ID, vos["vendor-b"].ID, "", domain.RefundReasonDispute, 1000))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = f.uc.ResolveSupportCase(t.Context(), "admin-1", sc.ID, usecase.ResolveInput{Kind: domain.ResolutionRefund,
		LinkedOperationID: otherShop.ID, Reason: "x", ExpectedVersion: sc.Version})
	expectCode(t, err, domain.CodeSupportOperationInvalid)
	_, _, err = f.uc.ResolveSupportCase(t.Context(), "admin-1", sc.ID, usecase.ResolveInput{Kind: domain.ResolutionRefund,
		Reason: "x", ExpectedVersion: sc.Version})
	expectCode(t, err, apperror.CodeValidation)
}

func TestSupportCase_ReturnOutcomeDrivesResolution(t *testing.T) {
	f := newCheckoutFixture()
	order, vo, sc := openCase(t, f)
	sc = assign(t, f, sc.ID, admin1)
	rr, err := f.uc.CreateReturn(t.Context(), "buyer-1", usecase.ReturnInput{OrderID: order.ID, ItemID: itemOf(f, vo).ID, Reason: "Hỏng"})
	if err != nil {
		t.Fatal(err)
	}
	sc, pending, err := f.uc.ResolveSupportCase(t.Context(), "admin-1", sc.ID, usecase.ResolveInput{Kind: domain.ResolutionReturn,
		LinkedOperationID: rr.ID, Reason: "Trả hàng hoàn tiền", ExpectedVersion: sc.Version})
	if err != nil || !pending {
		t.Fatalf("expected pending, got %v %v", err, pending)
	}
	if _, err := f.uc.AdminDecideReturn(t.Context(), "admin-1", rr.ID, false, "Không đủ bằng chứng"); err != nil {
		t.Fatal(err)
	}
	if got := f.support.get(sc.ID); got.Status != domain.CaseInProgress {
		t.Fatalf("a rejected return sends the case back to the admin, got %s", got.Status)
	}
}

func TestSupportCase_ResolvedNeedsAnAssignedCaseAndNoActionResolvesAtOnce(t *testing.T) {
	f := newCheckoutFixture()
	_, _, sc := openCase(t, f)
	_, _, err := f.uc.ResolveSupportCase(t.Context(), "admin-1", sc.ID, usecase.ResolveInput{Kind: domain.ResolutionNoAction,
		Reason: "x", ExpectedVersion: sc.Version})
	expectCode(t, err, apperror.CodeConflict)
	sc = assign(t, f, sc.ID, admin1)
	sc, pending, err := f.uc.ResolveSupportCase(t.Context(), "admin-1", sc.ID, usecase.ResolveInput{Kind: domain.ResolutionNoAction,
		Reason: "Đơn vị vận chuyển xác nhận đã giao", ExpectedVersion: sc.Version})
	if err != nil || pending || sc.Status != domain.CaseResolved {
		t.Fatalf("no_action resolves at once, got %v %v %+v", err, pending, sc)
	}
}

func TestSupportCase_OneOpenCasePerVendorOrderAndCategory_AndIdempotentCreate(t *testing.T) {
	f := newCheckoutFixture()
	order, vos := deliveredOrder(t, f)
	in := usecase.CreateSupportCaseInput{OrderID: order.ID, VendorOrderID: vos["vendor-a"].ID, Category: "damaged",
		Message: "Hộp bị móp", IdempotencyKey: "case-key-0001"}
	first, _, err := f.uc.CreateSupportCase(t.Context(), "buyer-1", in)
	if err != nil {
		t.Fatal(err)
	}
	again, replayed, err := f.uc.CreateSupportCase(t.Context(), "buyer-1", in)
	if err != nil || !replayed || again.ID != first.ID {
		t.Fatalf("a resend with the same key returns the same case, got %v replayed=%v", err, replayed)
	}
	changed := in
	changed.Message = "Khác"
	_, _, err = f.uc.CreateSupportCase(t.Context(), "buyer-1", changed)
	expectCode(t, err, domain.CodeSupportKeyReused)

	in.IdempotencyKey = ""
	_, _, err = f.uc.CreateSupportCase(t.Context(), "buyer-1", in)
	expectCode(t, err, domain.CodeCaseAlreadyOpen)

	// Another category or the other shop is a separate case.
	in.Category = "missing_items"
	if _, _, err := f.uc.CreateSupportCase(t.Context(), "buyer-1", in); err != nil {
		t.Fatal(err)
	}
	in.VendorOrderID = vos["vendor-b"].ID
	if _, _, err := f.uc.CreateSupportCase(t.Context(), "buyer-1", in); err != nil {
		t.Fatal(err)
	}
}

func TestSupportCase_OnlyTheBuyerOwnOrderAndPaidGoods(t *testing.T) {
	f := newCheckoutFixture()
	order := placedOrder(t, f)
	vos, _ := f.vendorOrders.ListByOrderID(t.Context(), order.ID)
	in := usecase.CreateSupportCaseInput{OrderID: order.ID, VendorOrderID: vos[0].ID, Category: "not_received", Message: "?"}

	_, _, err := f.uc.CreateSupportCase(t.Context(), "buyer-2", in)
	expectCode(t, err, apperror.CodeNotFound)
	_, _, err = f.uc.CreateSupportCase(t.Context(), "buyer-1", in)
	expectCode(t, err, apperror.CodeConflict) // unpaid: only a payment question
	in.Category = "payment_issue"
	sc, _, err := f.uc.CreateSupportCase(t.Context(), "buyer-1", in)
	if err != nil {
		t.Fatal(err)
	}
	if !sc.FinancialHold {
		t.Fatal("a payment question may end in a refund: it holds the payout")
	}
	in.Category, in.Message = "other", "<script>alert(1)</script>"
	_, _, err = f.uc.CreateSupportCase(t.Context(), "buyer-1", in)
	expectCode(t, err, apperror.CodeValidation)
}

func TestSupportCase_CrossAccessIsNotFound(t *testing.T) {
	f := newCheckoutFixture()
	_, _, sc := openCase(t, f)
	for _, actor := range []usecase.SupportActor{{ID: "buyer-2", Role: "buyer"}, vendorB, {ID: "user-x", Role: "vendor"}} {
		_, err := f.uc.GetSupportCase(t.Context(), actor, sc.ID)
		expectCode(t, err, apperror.CodeNotFound)
		_, _, err = f.uc.PostSupportMessage(t.Context(), actor, sc.ID, usecase.SupportMessageInput{Text: "hi"})
		expectCode(t, err, apperror.CodeNotFound)
	}
	page, err := f.uc.ListVendorSupportCases(t.Context(), "user-b", "vendor-b", "", "", 20)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("another shop must not list this case, got %v %d", err, len(page.Items))
	}
	_, err = f.uc.ListVendorSupportCases(t.Context(), "user-b", "vendor-a", "", "", 20)
	expectCode(t, err, apperror.CodeForbidden)

	page, err = f.uc.ListVendorSupportCases(t.Context(), "user-a", "vendor-a", "", "", 20)
	if err != nil || len(page.Items) != 1 || page.Items[0].BuyerID != "" || page.Items[0].AssigneeID != nil {
		t.Fatalf("the selling shop lists its case without the buyer id, got %v %+v", err, page.Items)
	}
}

func TestSupportCase_InternalNotesNeverReachBuyerOrVendor(t *testing.T) {
	f := newCheckoutFixture()
	_, _, sc := openCase(t, f)
	assign(t, f, sc.ID, admin1)
	note, err := f.uc.UploadSupportAttachment(t.Context(), admin1, pngBytes(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.uc.PostSupportMessage(t.Context(), admin1, sc.ID, usecase.SupportMessageInput{Text: "Shop này hay giao chậm",
		Visibility: domain.VisibilityInternal, AttachmentIDs: []string{note.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.uc.PostSupportMessage(t.Context(), vendorA, sc.ID, usecase.SupportMessageInput{Text: "x",
		Visibility: domain.VisibilityInternal}); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []usecase.SupportActor{buyer, vendorA} {
		d, err := f.uc.GetSupportCase(t.Context(), actor, sc.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range d.Messages {
			if m.Visibility != domain.VisibilityPublic || strings.Contains(m.Text, "giao chậm") {
				t.Fatalf("%s sees an internal note: %+v", actor.Role, m)
			}
			if m.AuthorRole == "admin" && m.AuthorID != "" {
				t.Fatalf("%s sees an admin id", actor.Role)
			}
		}
		for _, e := range d.Events {
			if e.Note != nil || (e.ActorRole == "admin" && e.ActorID != nil) {
				t.Fatalf("%s sees admin timeline details: %+v", actor.Role, e)
			}
		}
		if d.Case.AssigneeID != nil {
			t.Fatalf("%s sees the assignee", actor.Role)
		}
		_, err = f.uc.OpenSupportAttachment(t.Context(), actor, sc.ID, note.ID)
		expectCode(t, err, apperror.CodeNotFound)
	}
	d, _ := f.uc.GetSupportCase(t.Context(), admin1, sc.ID)
	internal := 0
	for _, m := range d.Messages {
		if m.Visibility == domain.VisibilityInternal {
			internal++
		}
	}
	if internal != 1 {
		t.Fatalf("admins see the internal note, got %d", internal)
	}
	if file, err := f.uc.OpenSupportAttachment(t.Context(), admin1, sc.ID, note.ID); err != nil {
		t.Fatal(err)
	} else {
		_ = file.Body.Close()
	}
}

func TestSupportCase_ConcurrentAssignmentOneWins(t *testing.T) {
	f := newCheckoutFixture()
	_, _, sc := openCase(t, f)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, admin := range []usecase.SupportActor{admin1, admin2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.uc.AssignSupportCase(t.Context(), admin.ID, sc.ID, admin.ID, sc.Version, "Take responsibility")
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
			continue
		}
		expectCode(t, err, domain.CodeVersionConflict)
	}
	if wins != 1 {
		t.Fatalf("exactly one admin must win, got %d (%v)", wins, errs)
	}
	f.identity.denied["user-a"] = true
	current := f.support.get(sc.ID)
	_, err := f.uc.AssignSupportCase(t.Context(), "admin-1", sc.ID, "user-a", current.Version, "Take responsibility")
	expectCode(t, err, apperror.CodeValidation)
}

func TestSupportCase_WaitingStatesFollowReplies(t *testing.T) {
	f := newCheckoutFixture()
	_, _, sc := openCase(t, f)
	sc = assign(t, f, sc.ID, admin1)
	sc, err := f.uc.ChangeSupportCaseStatus(t.Context(), "admin-1", sc.ID, "waiting_vendor", sc.Version, "Cần mã vận đơn")
	if err != nil || sc.Status != domain.CaseWaitingVendor || sc.DueAt == nil {
		t.Fatalf("got %v %+v", err, sc)
	}
	if _, _, err := f.uc.PostSupportMessage(t.Context(), vendorA, sc.ID, usecase.SupportMessageInput{Text: "Mã vận đơn ABC"}); err != nil {
		t.Fatal(err)
	}
	sc = f.support.get(sc.ID)
	if sc.Status != domain.CaseInProgress {
		t.Fatalf("the shop's answer puts the case back in progress, got %s", sc.Status)
	}
	sc, err = f.uc.ChangeSupportCaseStatus(t.Context(), "admin-1", sc.ID, "waiting_buyer", sc.Version, "")
	if err != nil || sc.DueAt != nil {
		t.Fatalf("no marketplace deadline while waiting for the buyer, got %v %+v", err, sc)
	}
	if _, _, err := f.uc.PostSupportMessage(t.Context(), buyer, sc.ID, usecase.SupportMessageInput{Text: "Đây là ảnh"}); err != nil {
		t.Fatal(err)
	}
	if got := f.support.get(sc.ID).Status; got != domain.CaseInProgress {
		t.Fatalf("got %s", got)
	}
	_, err = f.uc.ChangeSupportCaseStatus(t.Context(), "admin-1", sc.ID, "waiting_buyer", sc.Version, "")
	expectCode(t, err, domain.CodeVersionConflict)
}

func TestSupportCase_ReopenWindowAndAutoClose(t *testing.T) {
	f := newCheckoutFixture()
	order, vo, sc := openCase(t, f)
	sc = assign(t, f, sc.ID, admin1)
	sc, _, err := f.uc.ResolveSupportCase(t.Context(), "admin-1", sc.ID, usecase.ResolveInput{Kind: domain.ResolutionNoAction,
		Reason: "Đã giao", ExpectedVersion: sc.Version})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = f.uc.PostSupportMessage(t.Context(), buyer, sc.ID, usecase.SupportMessageInput{Text: "Không đúng"})
	expectCode(t, err, apperror.CodeConflict)
	sc, err = f.uc.ReopenSupportCase(t.Context(), "buyer-1", sc.ID, "Tôi vẫn chưa nhận được")
	if err != nil || sc.Status != domain.CaseInProgress || sc.ResolutionKind != nil {
		t.Fatalf("reopen within the window, got %v %+v", err, sc)
	}
	sc, _, err = f.uc.ResolveSupportCase(t.Context(), "admin-1", sc.ID, usecase.ResolveInput{Kind: domain.ResolutionNoAction,
		Reason: "Đã giao lại", ExpectedVersion: sc.Version})
	if err != nil {
		t.Fatal(err)
	}

	f.now = f.now.Add(8 * 24 * time.Hour)
	_, err = f.uc.ReopenSupportCase(t.Context(), "buyer-1", sc.ID, "Vẫn chưa")
	expectCode(t, err, apperror.CodeConflict)
	// A new case on the same topic closes the expired one and links to it.
	again, _, err := f.uc.CreateSupportCase(t.Context(), "buyer-1", usecase.CreateSupportCaseInput{OrderID: order.ID,
		VendorOrderID: vo.ID, Category: "not_received", Message: "Mở lại"})
	if err != nil || again.RelatedCaseID == nil || *again.RelatedCaseID != sc.ID {
		t.Fatalf("expected a new case linked to the old one, got %v %+v", err, again)
	}
	if got := f.support.get(sc.ID).Status; got != domain.CaseClosed {
		t.Fatalf("the expired case must be closed, got %s", got)
	}

	// The worker sweep closes resolved cases past the window.
	again = assign(t, f, again.ID, admin1)
	if _, _, err := f.uc.ResolveSupportCase(t.Context(), "admin-1", again.ID, usecase.ResolveInput{Kind: domain.ResolutionNoAction,
		Reason: "ok", ExpectedVersion: again.Version}); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(8 * 24 * time.Hour)
	if n, err := f.uc.CloseExpiredSupportCases(t.Context(), 10); err != nil || n != 1 {
		t.Fatalf("expected one auto-close, got %d %v", n, err)
	}
	if got := f.support.get(again.ID).Status; got != domain.CaseClosed {
		t.Fatalf("got %s", got)
	}
}

func TestSupportCase_FeatureOffStopsNewCasesOnly(t *testing.T) {
	f := newCheckoutFixture()
	order, vo, sc := openCase(t, f)
	f.uc.SupportConfig.Enabled = false
	_, _, err := f.uc.CreateSupportCase(t.Context(), "buyer-1", usecase.CreateSupportCaseInput{OrderID: order.ID,
		VendorOrderID: vo.ID, Category: "damaged", Message: "x"})
	expectCode(t, err, domain.CodeSupportDisabled)
	if _, _, err := f.uc.PostSupportMessage(t.Context(), buyer, sc.ID, usecase.SupportMessageInput{Text: "vẫn trả lời được"}); err != nil {
		t.Fatalf("existing cases stay answerable: %v", err)
	}
	assign(t, f, sc.ID, admin1)

	f.uc.SupportConfig.Enabled = true
	f.uc.SupportConfig.PilotVendorIDs = map[string]bool{"vendor-b": true}
	_, _, err = f.uc.CreateSupportCase(t.Context(), "buyer-1", usecase.CreateSupportCaseInput{OrderID: order.ID,
		VendorOrderID: vo.ID, Category: "damaged", Message: "x"})
	expectCode(t, err, domain.CodeSupportDisabled)
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// jpegWithExif is a JPEG carrying an APP1 Exif segment (e.g. GPS data).
func jpegWithExif(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	exif := append([]byte{0xFF, 0xE1, 0x00, 0x10}, []byte("Exif\x00\x00GPS-SECRET")...)
	return append(append(append([]byte{}, raw[:2]...), exif...), raw[2:]...)
}

func TestSupportAttachment_OnlyRealImagesWithoutMetadata(t *testing.T) {
	f := newCheckoutFixture()
	_, err := f.uc.UploadSupportAttachment(t.Context(), buyer, []byte("%PDF-1.4 not an image"))
	expectCode(t, err, domain.CodeUnsupportedAttachment)
	if mustAppError(t, err).Status != 422 {
		t.Fatal("a file that is not an image is 422")
	}
	// An HTML file named .png is still not an image.
	_, err = f.uc.UploadSupportAttachment(t.Context(), buyer, []byte("<html><script>x</script></html>"))
	expectCode(t, err, domain.CodeUnsupportedAttachment)
	_, err = f.uc.UploadSupportAttachment(t.Context(), buyer, append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...))
	expectCode(t, err, domain.CodeUnsupportedAttachment)
	_, err = f.uc.UploadSupportAttachment(t.Context(), buyer, make([]byte, 5<<20+1))
	expectCode(t, err, domain.CodeUnsupportedAttachment)

	a, err := f.uc.UploadSupportAttachment(t.Context(), buyer, jpegWithExif(t))
	if err != nil {
		t.Fatal(err)
	}
	stored := f.store.objects[f.support.attachments[a.ID].ObjectKey]
	if a.ContentType != "image/jpeg" || bytes.Contains(stored, []byte("GPS-SECRET")) || bytes.Contains(stored, []byte("Exif")) {
		t.Fatal("the stored image must be re-encoded without its metadata")
	}
}

func TestSupportAttachment_AttachOnceByItsOwner_AndOrphansAreCleaned(t *testing.T) {
	f := newCheckoutFixture()
	order, vo := func() (*domain.Order, *domain.VendorOrder) { o, vos := deliveredOrder(t, f); return o, vos["vendor-a"] }()
	a, err := f.uc.UploadSupportAttachment(t.Context(), buyer, pngBytes(t))
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := f.uc.UploadSupportAttachment(t.Context(), vendorA, pngBytes(t))
	if err != nil {
		t.Fatal(err)
	}
	in := usecase.CreateSupportCaseInput{OrderID: order.ID, VendorOrderID: vo.ID, Category: "damaged", Message: "Ảnh hàng hỏng",
		AttachmentIDs: []string{foreign.ID}}
	_, _, err = f.uc.CreateSupportCase(t.Context(), "buyer-1", in)
	expectCode(t, err, apperror.CodeValidation)

	in.AttachmentIDs = []string{a.ID}
	sc, _, err := f.uc.CreateSupportCase(t.Context(), "buyer-1", in)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = f.uc.PostSupportMessage(t.Context(), buyer, sc.ID, usecase.SupportMessageInput{Text: "lại", AttachmentIDs: []string{a.ID}})
	expectCode(t, err, apperror.CodeValidation)

	for _, actor := range []usecase.SupportActor{buyer, vendorA, admin1} {
		file, err := f.uc.OpenSupportAttachment(t.Context(), actor, sc.ID, a.ID)
		if err != nil {
			t.Fatalf("%s reads the public evidence: %v", actor.Role, err)
		}
		data, _ := io.ReadAll(file.Body)
		_ = file.Body.Close()
		if len(data) == 0 || file.ContentType != "image/png" {
			t.Fatal("expected the image")
		}
	}

	f.now = f.now.Add(25 * time.Hour)
	if n, err := f.uc.CleanSupportAttachments(t.Context(), 10); err != nil || n != 1 {
		t.Fatalf("only the unattached upload is an orphan, got %d %v", n, err)
	}
	if _, ok := f.store.objects[f.support.attachments[foreign.ID].ObjectKey]; ok {
		t.Fatal("the orphan object must be deleted")
	}
	if _, ok := f.store.objects[f.support.attachments[a.ID].ObjectKey]; !ok {
		t.Fatal("attached evidence is kept while the case is open")
	}
}

func TestSupportCase_ListPagesWithACursor(t *testing.T) {
	f := newCheckoutFixture()
	order, vos := deliveredOrder(t, f)
	for _, category := range []string{"not_received", "damaged", "missing_items"} {
		if _, _, err := f.uc.CreateSupportCase(t.Context(), "buyer-1", usecase.CreateSupportCaseInput{OrderID: order.ID,
			VendorOrderID: vos["vendor-a"].ID, Category: category, Message: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := f.uc.ListMySupportCases(t.Context(), "buyer-1", "", "", 2)
	if err != nil || len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("got %v %+v", err, page)
	}
	next, err := f.uc.ListMySupportCases(t.Context(), "buyer-1", "", page.NextCursor, 2)
	if err != nil || len(next.Items) != 1 || next.NextCursor != "" || next.Items[0].ID == page.Items[0].ID {
		t.Fatalf("got %v %+v", err, next)
	}
	_, err = f.uc.ListMySupportCases(t.Context(), "buyer-1", "", "not-a-cursor", 2)
	expectCode(t, err, apperror.CodeValidation)
	queue, err := f.uc.ListSupportCases(t.Context(), "admin-1", usecase.AdminSupportFilter{Unassigned: true}, "", 20)
	if err != nil || len(queue.Items) != 3 || queue.Items[0].BuyerID == "" {
		t.Fatalf("admins see the whole unassigned queue, got %v %d", err, len(queue.Items))
	}
}
