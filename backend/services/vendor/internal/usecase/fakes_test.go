package usecase_test

import (
	"context"
	"strconv"
	"sync"
	"time"

	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/repository"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

type fakeVendorRepository struct {
	mu     sync.Mutex
	byID   map[string]*domain.Vendor
	nextID int
}

func (f *fakeVendorRepository) Operations(context.Context) (*domain.OperationsSummary, error) {
	return &domain.OperationsSummary{}, nil
}

func newFakeVendorRepository() *fakeVendorRepository {
	return &fakeVendorRepository{byID: make(map[string]*domain.Vendor)}
}

func (f *fakeVendorRepository) Create(_ context.Context, v *domain.Vendor) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.nextID++
	v.ID = "vendor-" + strconv.Itoa(f.nextID)
	v.Version = 1
	v.Status = domain.StatusPending
	v.CreatedAt = time.Now()
	v.UpdatedAt = time.Now()
	stored := *v
	f.byID[v.ID] = &stored
	return nil
}

// ListByUserID returns every shop a user owns (0, 1, or many) — a user may
// own several under the 1:N vendor<->user relationship.
func (f *fakeVendorRepository) ListByUserID(_ context.Context, userID string, limit, offset int) ([]*domain.Vendor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []*domain.Vendor
	for _, v := range f.byID {
		if v.UserID == userID {
			copyV := *v
			out = append(out, &copyV)
		}
	}
	return out, nil
}

func (f *fakeVendorRepository) FindByID(_ context.Context, id string) (*domain.Vendor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	v, ok := f.byID[id]
	if !ok {
		return nil, repository.ErrVendorNotFound
	}
	copyV := *v
	return &copyV, nil
}

func (f *fakeVendorRepository) ListByStatus(_ context.Context, status string, _, _ int) ([]*domain.Vendor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []*domain.Vendor
	for _, v := range f.byID {
		if status == "" || string(v.Status) == status {
			copyV := *v
			out = append(out, &copyV)
		}
	}
	return out, nil
}

func (f *fakeVendorRepository) ListByIDs(_ context.Context, ids []string) ([]*domain.Vendor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}

	var out []*domain.Vendor
	for id, v := range f.byID {
		if wanted[id] {
			copyV := *v
			out = append(out, &copyV)
		}
	}
	return out, nil
}

func (f *fakeVendorRepository) UpdateProfile(_ context.Context, id, shopName, description, policyText string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	v, ok := f.byID[id]
	if !ok {
		return repository.ErrVendorNotFound
	}
	v.ShopName = shopName
	v.Description = description
	v.PolicyText = policyText
	return nil
}

func (f *fakeVendorRepository) SetLogo(_ context.Context, id string, url, objectKey *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	v, ok := f.byID[id]
	if !ok {
		return repository.ErrVendorNotFound
	}
	v.LogoURL, v.LogoObjectKey = url, objectKey
	return nil
}

func (f *fakeVendorRepository) SetBanner(_ context.Context, id string, url, objectKey *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	v, ok := f.byID[id]
	if !ok {
		return repository.ErrVendorNotFound
	}
	v.BannerURL, v.BannerObjectKey = url, objectKey
	return nil
}

func (f *fakeVendorRepository) UpdateStatus(_ context.Context, id string, version int64, status domain.Status, approvedBy string, rejectionReason *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	v, ok := f.byID[id]
	if !ok {
		return repository.ErrVendorNotFound
	}
	if v.Version != version {
		return repository.ErrStaleVendor
	}
	v.Version++
	v.Status = status
	v.ApprovedBy = &approvedBy
	v.RejectionReason = rejectionReason
	return nil
}

type auditEntry struct {
	VendorID    string
	ActorUserID string
	Action      string
	Reason      *string
}

type fakeAuditLogRepository struct {
	mu      sync.Mutex
	entries []auditEntry
}

func newFakeAuditLogRepository() *fakeAuditLogRepository {
	return &fakeAuditLogRepository{}
}

func (f *fakeAuditLogRepository) Create(_ context.Context, vendorID, actorUserID, action string, reason *string, version int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, auditEntry{VendorID: vendorID, ActorUserID: actorUserID, Action: action, Reason: reason})
	return nil
}

// List returns entries newest-first, matching the real repository's
// ORDER BY created_at DESC -- entries carry no timestamp here, so append
// order stands in for creation order (reversed).
func (f *fakeAuditLogRepository) List(_ context.Context, vendorID string, limit, offset int) ([]*domain.AuditLog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []*domain.AuditLog
	for i := len(f.entries) - 1; i >= 0; i-- {
		e := f.entries[i]
		if e.VendorID != vendorID {
			continue
		}
		out = append(out, &domain.AuditLog{ActorUserID: e.ActorUserID, Action: e.Action, Reason: e.Reason, CreatedAt: time.Now()})
	}
	return out, nil
}

type fakeNotices struct{}

func (fakeNotices) Queue(context.Context, string, string, string, int64) error { return nil }

// fakeObjectStore simulates the object store for logo/banner uploads --
// Upload just records the key, Delete records which keys were removed so a
// test can assert a replaced image's old blob was cleaned up.
type fakeObjectStore struct {
	mu      sync.Mutex
	uploads map[string][]byte
	deleted []string
}

func newFakeObjectStore() *fakeObjectStore {
	return &fakeObjectStore{uploads: make(map[string][]byte)}
}

func (f *fakeObjectStore) Upload(_ context.Context, objectKey string, data []byte, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads[objectKey] = data
	return "https://objectstore.local/" + objectKey, nil
}

func (f *fakeObjectStore) Delete(_ context.Context, objectKey string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.uploads, objectKey)
	f.deleted = append(f.deleted, objectKey)
	return nil
}

type fakeVendorAddressRepository struct {
	mu     sync.Mutex
	byID   map[string]*domain.VendorAddress
	nextID int
}

func newFakeVendorAddressRepository() *fakeVendorAddressRepository {
	return &fakeVendorAddressRepository{byID: map[string]*domain.VendorAddress{}}
}

func (f *fakeVendorAddressRepository) Create(_ context.Context, a *domain.VendorAddress) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	a.ID = "address-" + strconv.Itoa(f.nextID)
	cp := *a
	f.byID[a.ID] = &cp
	return nil
}

func (f *fakeVendorAddressRepository) FindByID(_ context.Context, id string) (*domain.VendorAddress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.byID[id]
	if !ok {
		return nil, repository.ErrVendorAddressNotFound
	}
	cp := *a
	return &cp, nil
}

func (f *fakeVendorAddressRepository) ListForVendor(_ context.Context, vendorID string, limit, offset int) ([]*domain.VendorAddress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.VendorAddress
	for _, a := range f.byID {
		if a.VendorID == vendorID {
			cp := *a
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakeVendorAddressRepository) FindDefaultForVendor(_ context.Context, vendorID string) (*domain.VendorAddress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.byID {
		if a.VendorID == vendorID && a.IsDefault {
			cp := *a
			return &cp, nil
		}
	}
	return nil, repository.ErrVendorAddressNotFound
}

func (f *fakeVendorAddressRepository) Update(_ context.Context, id string, a *domain.VendorAddress) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	existing, ok := f.byID[id]
	if !ok {
		return repository.ErrVendorAddressNotFound
	}
	existing.RecipientName, existing.Phone = a.RecipientName, a.Phone
	existing.Province, existing.District, existing.Ward, existing.StreetAddress = a.Province, a.District, a.Ward, a.StreetAddress
	return nil
}

func (f *fakeVendorAddressRepository) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.byID[id]; !ok {
		return repository.ErrVendorAddressNotFound
	}
	delete(f.byID, id)
	return nil
}

func (f *fakeVendorAddressRepository) SetDefault(_ context.Context, vendorID, addressID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	found := false
	for _, a := range f.byID {
		if a.ID == addressID && a.VendorID == vendorID {
			found = true
		}
	}
	if !found {
		return repository.ErrVendorAddressNotFound
	}
	for _, a := range f.byID {
		if a.VendorID == vendorID {
			a.IsDefault = a.ID == addressID
		}
	}
	return nil
}

func (f *fakeVendorRepository) Snapshots(ctx context.Context, after string, limit int) ([]*domain.Vendor, error) {
	return f.ListByStatus(ctx, "", limit, 0)
}

type directTx struct{}

func (directTx) Run(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type allowActor struct{}

func (allowActor) RequireRole(context.Context, string, string) error { return nil }

type fakeEvents struct{}

func (fakeEvents) Queue(context.Context, *domain.Vendor) error { return nil }
func (fakeEvents) Replay(context.Context, string) error        { return nil }

type readyAddresses struct{ *fakeVendorAddressRepository }

func (readyAddresses) FindDefaultForVendor(ctx context.Context, id string) (*domain.VendorAddress, error) {
	return &domain.VendorAddress{VendorID: id, RecipientName: "Test owner", Phone: "0900000000", Province: "Test province", District: "Test district", Ward: "Test ward", StreetAddress: "Test street", IsDefault: true}, nil
}
func testOps() usecase.Operations {
	return usecase.Operations{Tx: directTx{}, Actors: allowActor{}, Addresses: readyAddresses{newFakeVendorAddressRepository()}, Events: fakeEvents{}, Notices: fakeNotices{}}
}
