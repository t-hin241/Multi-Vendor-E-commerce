package repository_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"shopee/backend/services/review/internal/domain"
	"shopee/backend/services/review/internal/repository"
)

// The admin and shop queries no other test reaches: filters, the vendor
// summary, moderation reasons, report lists and stored images.
func TestAdminAndShopQueriesIntegration(t *testing.T) {
	pool := reviewDB(t)
	ctx := t.Context()
	repo := repository.NewReviewRepository(pool)

	vendor, otherVendor := uuid.NewString(), uuid.NewString()
	productA, productB := uuid.NewString(), uuid.NewString()
	buyer := uuid.NewString()
	create := func(vendorID, productID, buyerID string, rating int, verified bool) *domain.Review {
		t.Helper()
		v := &domain.Review{BuyerID: buyerID, VendorID: vendorID, ProductID: productID, OrderItemID: uuid.NewString(),
			VendorOrderID: uuid.NewString(), Rating: rating, Comment: "Integration test review", VerifiedPurchase: verified}
		if err := repo.Create(ctx, v, true); err != nil {
			t.Fatal(err)
		}
		return v
	}
	r1 := create(vendor, productA, buyer, 5, true)
	r2 := create(vendor, productB, uuid.NewString(), 2, true)
	r3 := create(vendor, productA, uuid.NewString(), 4, false)
	r4 := create(otherVendor, productA, buyer, 1, true)

	reason := &domain.Reason{Code: "spam", Label: "Spam"}
	if err := repo.CreateReason(ctx, reason); err != nil {
		t.Fatal(err)
	}
	if err := repo.Hide(ctx, r2.ID, reason.ID, uuid.NewString(), nil); err != nil {
		t.Fatal(err)
	}

	ids := func(reviews []*domain.Review) []string {
		out := []string{}
		for _, v := range reviews {
			out = append(out, v.ID)
		}
		return out
	}
	for _, tc := range []struct {
		name   string
		filter repository.AdminFilter
		want   []string
	}{
		{"everything, newest first", repository.AdminFilter{}, []string{r4.ID, r3.ID, r2.ID, r1.ID}},
		{"buyer", repository.AdminFilter{BuyerID: buyer}, []string{r4.ID, r1.ID}},
		{"vendor and product", repository.AdminFilter{VendorID: vendor, ProductID: productA}, []string{r3.ID, r1.ID}},
		{"hidden", repository.AdminFilter{Status: "hidden"}, []string{r2.ID}},
		{"rating", repository.AdminFilter{Rating: 5}, []string{r1.ID}},
	} {
		got, err := repo.ListAdmin(ctx, tc.filter, 20, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(ids(got), tc.want) {
			t.Errorf("ListAdmin %s = %v, want %v", tc.name, ids(got), tc.want)
		}
	}
	if page, err := repo.ListAdmin(ctx, repository.AdminFilter{}, 2, 1); err != nil || !slices.Equal(ids(page), []string{r3.ID, r2.ID}) {
		t.Errorf("ListAdmin page = %v, %v", ids(page), err)
	}

	// Published only (r2 is hidden); unverified (r3) only when asked.
	summary, err := repo.VendorSummary(ctx, vendor, false)
	if err != nil || summary.RatingCount != 1 || summary.RatingAverage != 5 || summary.Distribution != [5]int64{0, 0, 0, 0, 1} {
		t.Errorf("VendorSummary verified only = %+v, %v", summary, err)
	}
	summary, err = repo.VendorSummary(ctx, vendor, true)
	if err != nil || summary.RatingCount != 2 || summary.RatingAverage != 4.5 || summary.Distribution != [5]int64{0, 0, 0, 1, 1} {
		t.Errorf("VendorSummary with unverified = %+v, %v", summary, err)
	}
	if empty, err := repo.VendorSummary(ctx, uuid.NewString(), true); err != nil || empty != (domain.Summary{}) {
		t.Errorf("VendorSummary of a shop without reviews = %+v, %v", empty, err)
	}

	// Moderation reasons: active filter, label order, update conflicts.
	other := &domain.Reason{Code: "abuse", Label: "Abuse"}
	if err := repo.CreateReason(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateReason(ctx, &domain.Reason{Code: "spam", Label: "Again"}); !errors.Is(err, repository.ErrConflict) {
		t.Errorf("duplicate reason code: %v", err)
	}
	created := other.UpdatedAt
	other.IsActive = false
	if err := repo.UpdateReason(ctx, other); err != nil || !other.UpdatedAt.After(created) {
		t.Errorf("UpdateReason = %v, updated_at %v -> %v", err, created, other.UpdatedAt)
	}
	reasonCodes := func(activeOnly bool) []string {
		t.Helper()
		list, err := repo.ListReasons(ctx, activeOnly)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, x := range list {
			out = append(out, x.Code)
		}
		return out
	}
	if got := reasonCodes(false); !slices.Equal(got, []string{"abuse", "spam"}) {
		t.Errorf("all reasons = %v", got)
	}
	if got := reasonCodes(true); !slices.Equal(got, []string{"spam"}) {
		t.Errorf("active reasons = %v", got)
	}
	if found, err := repo.FindReason(ctx, other.ID); err != nil || found.IsActive || found.Label != "Abuse" {
		t.Errorf("FindReason = %+v, %v", found, err)
	}
	if err := repo.UpdateReason(ctx, &domain.Reason{ID: other.ID, Code: "spam", Label: "Abuse"}); !errors.Is(err, repository.ErrConflict) {
		t.Errorf("UpdateReason to a taken code: %v", err)
	}
	if err := repo.UpdateReason(ctx, &domain.Reason{ID: uuid.NewString(), Code: "gone", Label: "Gone"}); !errors.Is(err, repository.ErrNotFound) {
		t.Errorf("UpdateReason of an unknown reason: %v", err)
	}
	if _, err := repo.FindReason(ctx, uuid.NewString()); !errors.Is(err, repository.ErrNotFound) {
		t.Errorf("FindReason of an unknown reason: %v", err)
	}

	// Reports: filters by status, the reported review's shop and product.
	report := func(review *domain.Review) *domain.Report {
		t.Helper()
		x := &domain.Report{ReviewID: review.ID, ReportingVendorID: review.VendorID, ReasonID: reason.ID, ReasonCode: reason.Code,
			ReasonLabel: reason.Label}
		if err := repo.CreateReport(ctx, x); err != nil {
			t.Fatal(err)
		}
		return x
	}
	p1, p3, p4 := report(r1), report(r3), report(r4)
	if p1.Status != domain.ReportOpen {
		t.Errorf("new report status = %q", p1.Status)
	}
	if err := repo.ResolveReport(ctx, p3.ID, domain.DecisionKeep, nil, uuid.NewString(), nil); err != nil {
		t.Fatal(err)
	}
	reportIDs := func(status, vendorID, productID string) []string {
		t.Helper()
		list, err := repo.ListReports(ctx, status, vendorID, productID, 20, 0)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, x := range list {
			out = append(out, x.ID)
		}
		return out
	}
	if got := reportIDs("", "", ""); !slices.Equal(got, []string{p4.ID, p3.ID, p1.ID}) {
		t.Errorf("all reports = %v", got)
	}
	if got := reportIDs("open", vendor, ""); !slices.Equal(got, []string{p1.ID}) {
		t.Errorf("open reports of the shop = %v", got)
	}
	if got := reportIDs("", "", productA); !slices.Equal(got, []string{p4.ID, p3.ID, p1.ID}) {
		t.Errorf("reports of the product = %v", got)
	}
	resolved, err := repo.ListReports(ctx, "resolved", "", "", 20, 0)
	if err != nil || len(resolved) != 1 || resolved[0].Decision == nil || *resolved[0].Decision != domain.DecisionKeep || resolved[0].ResolvedAt == nil {
		t.Errorf("resolved reports = %+v, %v", resolved, err)
	}

	// Stored images come back in position order, with the reply alongside.
	for _, key := range []string{"reviews/test/a.jpg", "reviews/test/b.jpg"} {
		if err := repo.AddUpload(ctx, r1.ID, key); err != nil {
			t.Fatal(err)
		}
		if err := repo.RecordImage(ctx, &domain.Image{ReviewID: r1.ID, ObjectKey: key, URL: "https://media.example.invalid/" + key,
			ContentType: "image/jpeg", SizeBytes: 1234}); err != nil {
			t.Fatal(err)
		}
	}
	images, err := repo.ImagesFor(ctx, []string{r1.ID, r3.ID})
	if err != nil || len(images[r1.ID]) != 2 || len(images[r3.ID]) != 0 {
		t.Fatalf("ImagesFor = %v, %v", images, err)
	}
	if a, b := images[r1.ID][0], images[r1.ID][1]; a.Position != 0 || b.Position != 1 || a.ObjectKey != "reviews/test/a.jpg" ||
		a.URL != "https://media.example.invalid/reviews/test/a.jpg" || a.SizeBytes != 1234 || a.ID == "" || a.CreatedAt.IsZero() {
		t.Errorf("images = %+v, %+v", a, b)
	}
	if slots, err := repo.CountImageSlots(ctx, r1.ID); err != nil || slots != 2 {
		t.Errorf("CountImageSlots = %d, %v", slots, err)
	}
	if _, err := repo.SetReply(ctx, &domain.Reply{ReviewID: r1.ID, VendorID: vendor, Message: "Thanks"}); err != nil {
		t.Fatal(err)
	}
	replies, err := repo.RepliesFor(ctx, []string{r1.ID, r3.ID})
	if err != nil || len(replies) != 1 || replies[r1.ID].Message != "Thanks" || replies[r1.ID].VendorID != vendor {
		t.Errorf("RepliesFor = %v, %v", replies, err)
	}
}
