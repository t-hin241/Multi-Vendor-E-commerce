package usecase_test

import (
	"context"
	"strconv"
	"sync"
	"time"

	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/repository"
)

type fakeVendorRepository struct {
	mu     sync.Mutex
	byID   map[string]*domain.Vendor
	nextID int
}

func newFakeVendorRepository() *fakeVendorRepository {
	return &fakeVendorRepository{byID: make(map[string]*domain.Vendor)}
}

func (f *fakeVendorRepository) Create(_ context.Context, v *domain.Vendor) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, existing := range f.byID {
		if existing.UserID == v.UserID {
			return repository.ErrVendorAlreadyExists
		}
	}

	f.nextID++
	v.ID = "vendor-" + strconv.Itoa(f.nextID)
	v.Status = domain.StatusPending
	v.CreatedAt = time.Now()
	v.UpdatedAt = time.Now()
	stored := *v
	f.byID[v.ID] = &stored
	return nil
}

func (f *fakeVendorRepository) FindByUserID(_ context.Context, userID string) (*domain.Vendor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, v := range f.byID {
		if v.UserID == userID {
			copyV := *v
			return &copyV, nil
		}
	}
	return nil, repository.ErrVendorNotFound
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

func (f *fakeVendorRepository) UpdateProfile(_ context.Context, id, shopName, description string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	v, ok := f.byID[id]
	if !ok {
		return repository.ErrVendorNotFound
	}
	v.ShopName = shopName
	v.Description = description
	return nil
}

func (f *fakeVendorRepository) UpdateStatus(_ context.Context, id string, status domain.Status, approvedBy string, rejectionReason *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	v, ok := f.byID[id]
	if !ok {
		return repository.ErrVendorNotFound
	}
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

func (f *fakeAuditLogRepository) Create(_ context.Context, vendorID, actorUserID, action string, reason *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, auditEntry{VendorID: vendorID, ActorUserID: actorUserID, Action: action, Reason: reason})
	return nil
}

type sentNotification struct {
	userID      string
	notifType   string
	referenceID string
}

type fakeNotificationGateway struct {
	mu   sync.Mutex
	sent []sentNotification
}

func newFakeNotificationGateway() *fakeNotificationGateway {
	return &fakeNotificationGateway{}
}

func (f *fakeNotificationGateway) Notify(_ context.Context, userID, notifType, referenceID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentNotification{userID: userID, notifType: notifType, referenceID: referenceID})
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

func (f *fakeVendorAddressRepository) ListForVendor(_ context.Context, vendorID string) ([]*domain.VendorAddress, error) {
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
