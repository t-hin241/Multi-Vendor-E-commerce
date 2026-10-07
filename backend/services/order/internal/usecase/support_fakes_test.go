package usecase_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

// fakeSupportRepository mimics support_cases: copies in and out (so a
// stale version really is stale), the "one case not closed" unique index
// and compare-and-set saves.
type fakeSupportRepository struct {
	mu          sync.Mutex
	cases       map[string]*domain.SupportCase
	messages    []*domain.SupportMessage
	events      []*domain.SupportCaseEvent
	attachments map[string]*domain.CaseAttachment
	nextID      int
	seq         time.Time
}

func newFakeSupportRepository() *fakeSupportRepository {
	return &fakeSupportRepository{cases: map[string]*domain.SupportCase{}, attachments: map[string]*domain.CaseAttachment{},
		seq: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
}

func (f *fakeSupportRepository) id(prefix string) string {
	f.nextID++
	return fmt.Sprintf("%s-%08d-0000-0000-0000-000000000000", prefix, f.nextID)[:36]
}

func (f *fakeSupportRepository) tick() time.Time {
	f.seq = f.seq.Add(time.Second)
	return f.seq
}

func copyCase(c *domain.SupportCase) *domain.SupportCase {
	copied := *c
	return &copied
}

func (f *fakeSupportRepository) Create(_ context.Context, c *domain.SupportCase) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.cases {
		if c.IdempotencyKey != nil && existing.IdempotencyKey != nil && existing.BuyerID == c.BuyerID && *existing.IdempotencyKey == *c.IdempotencyKey {
			return repository.ErrSupportKeyTaken
		}
		if existing.BuyerID == c.BuyerID && existing.VendorOrderID == c.VendorOrderID && existing.Category == c.Category &&
			existing.Status != domain.CaseClosed {
			return domain.CaseAlreadyOpen()
		}
	}
	c.ID, c.Version, c.CreatedAt = f.id("case"), 1, f.tick()
	c.UpdatedAt = c.CreatedAt
	f.cases[c.ID] = copyCase(c)
	return nil
}

func (f *fakeSupportRepository) FindByID(_ context.Context, id string) (*domain.SupportCase, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.cases[id]
	if !ok {
		return nil, repository.ErrSupportCaseNotFound
	}
	return copyCase(c), nil
}

func (f *fakeSupportRepository) get(id string) *domain.SupportCase {
	c, _ := f.FindByID(context.Background(), id)
	return c
}

func (f *fakeSupportRepository) FindByIdempotencyKey(_ context.Context, buyerID, key string) (*domain.SupportCase, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.cases {
		if c.BuyerID == buyerID && c.IdempotencyKey != nil && *c.IdempotencyKey == key {
			return copyCase(c), nil
		}
	}
	return nil, nil
}

func (f *fakeSupportRepository) FindNotClosed(_ context.Context, buyerID, vendorOrderID string, category domain.SupportCategory) (*domain.SupportCase, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.cases {
		if c.BuyerID == buyerID && c.VendorOrderID == vendorOrderID && c.Category == category && c.Status != domain.CaseClosed {
			return copyCase(c), nil
		}
	}
	return nil, nil
}

func (f *fakeSupportRepository) Save(_ context.Context, c *domain.SupportCase) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	stored, ok := f.cases[c.ID]
	if !ok || stored.Version != c.Version {
		return repository.ErrStaleState
	}
	c.Version++
	c.UpdatedAt = f.tick()
	f.cases[c.ID] = copyCase(c)
	return nil
}

func (f *fakeSupportRepository) List(_ context.Context, filter repository.SupportCaseFilter, after *repository.CaseCursor, limit int) ([]*domain.SupportCase, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []*domain.SupportCase{}
	for _, c := range f.cases {
		switch {
		case filter.BuyerID != "" && c.BuyerID != filter.BuyerID,
			filter.VendorID != "" && c.VendorID != filter.VendorID,
			filter.Status != "" && string(c.Status) != filter.Status,
			filter.AssigneeID != "" && (c.AssigneeID == nil || *c.AssigneeID != filter.AssigneeID),
			filter.Unassigned && c.AssigneeID != nil,
			filter.OverdueAt != nil && (c.DueAt == nil || !c.DueAt.Before(*filter.OverdueAt)),
			after != nil && !(c.CreatedAt.Before(after.CreatedAt) || (c.CreatedAt.Equal(after.CreatedAt) && c.ID < after.ID)):
			continue
		}
		out = append(out, copyCase(c))
	}
	slices.SortFunc(out, func(a, b *domain.SupportCase) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		if a.ID > b.ID {
			return -1
		}
		return 1
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeSupportRepository) ListPendingResolution(_ context.Context, kind, ref string) ([]*domain.SupportCase, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []*domain.SupportCase{}
	for _, c := range f.cases {
		if c.Status == domain.CaseResolutionPending && c.ResolutionKind != nil && *c.ResolutionKind == kind && c.ResolutionRef != nil && *c.ResolutionRef == ref {
			out = append(out, copyCase(c))
		}
	}
	return out, nil
}

func (f *fakeSupportRepository) ListResolvedBefore(_ context.Context, t time.Time, limit int) ([]*domain.SupportCase, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []*domain.SupportCase{}
	for _, c := range f.cases {
		if c.Status == domain.CaseResolved && c.ResolvedAt != nil && c.ResolvedAt.Before(t) && len(out) < limit {
			out = append(out, copyCase(c))
		}
	}
	return out, nil
}

func (f *fakeSupportRepository) AddMessage(_ context.Context, m *domain.SupportMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.messages {
		if m.IdempotencyKey != nil && existing.IdempotencyKey != nil && existing.AuthorID == m.AuthorID && *existing.IdempotencyKey == *m.IdempotencyKey {
			return repository.ErrSupportKeyTaken
		}
	}
	m.ID, m.CreatedAt = f.id("msg0"), f.tick()
	copied := *m
	f.messages = append(f.messages, &copied)
	return nil
}

func (f *fakeSupportRepository) FindMessageByKey(_ context.Context, authorID, key string) (*domain.SupportMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.messages {
		if m.AuthorID == authorID && m.IdempotencyKey != nil && *m.IdempotencyKey == key {
			copied := *m
			return &copied, nil
		}
	}
	return nil, nil
}

func (f *fakeSupportRepository) ListMessages(_ context.Context, caseID string, includeInternal bool) ([]*domain.SupportMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []*domain.SupportMessage{}
	for _, m := range f.messages {
		if m.CaseID != caseID || (!includeInternal && m.Visibility != domain.VisibilityPublic) {
			continue
		}
		copied := *m
		copied.Attachments = []*domain.CaseAttachment{}
		for _, a := range f.attachments {
			if a.MessageID != nil && *a.MessageID == m.ID && a.State == domain.AttachmentAttached {
				attached := *a
				copied.Attachments = append(copied.Attachments, &attached)
			}
		}
		out = append(out, &copied)
	}
	return out, nil
}

func (f *fakeSupportRepository) AddEvent(_ context.Context, e *domain.SupportCaseEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e.ID, e.CreatedAt = f.id("evt0"), f.tick()
	copied := *e
	f.events = append(f.events, &copied)
	return nil
}

func (f *fakeSupportRepository) ListEvents(_ context.Context, caseID string) ([]*domain.SupportCaseEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []*domain.SupportCaseEvent{}
	for _, e := range f.events {
		if e.CaseID == caseID {
			copied := *e
			out = append(out, &copied)
		}
	}
	return out, nil
}

func (f *fakeSupportRepository) actions(caseID string) []string {
	events, _ := f.ListEvents(context.Background(), caseID)
	out := []string{}
	for _, e := range events {
		out = append(out, e.Action)
	}
	return out
}

func (f *fakeSupportRepository) CreateAttachment(_ context.Context, a *domain.CaseAttachment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a.ID, a.State, a.CreatedAt = f.id("att0"), domain.AttachmentUploaded, f.tick()
	copied := *a
	f.attachments[a.ID] = &copied
	return nil
}

func (f *fakeSupportRepository) FindAttachment(_ context.Context, id string) (*domain.CaseAttachment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.attachments[id]
	if !ok {
		return nil, repository.ErrAttachmentNotFound
	}
	copied := *a
	return &copied, nil
}

func (f *fakeSupportRepository) AttachToMessage(_ context.Context, ids []string, ownerID, caseID, messageID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, id := range ids {
		if a, ok := f.attachments[id]; ok && a.OwnerID == ownerID && a.State == domain.AttachmentUploaded {
			a.State, a.CaseID, a.MessageID = domain.AttachmentAttached, &caseID, &messageID
			n++
		}
	}
	return n, nil
}

func (f *fakeSupportRepository) ListOrphanAttachments(_ context.Context, before time.Time, _ int) ([]*domain.CaseAttachment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []*domain.CaseAttachment{}
	for _, a := range f.attachments {
		if a.State == domain.AttachmentUploaded && a.CreatedAt.Before(before) {
			copied := *a
			out = append(out, &copied)
		}
	}
	return out, nil
}

func (f *fakeSupportRepository) ListExpiredAttachments(_ context.Context, closedBefore time.Time, _ int) ([]*domain.CaseAttachment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []*domain.CaseAttachment{}
	for _, a := range f.attachments {
		if a.State != domain.AttachmentAttached || a.CaseID == nil {
			continue
		}
		if c := f.cases[*a.CaseID]; c.Status == domain.CaseClosed && c.ClosedAt != nil && c.ClosedAt.Before(closedBefore) {
			copied := *a
			out = append(out, &copied)
		}
	}
	return out, nil
}

func (f *fakeSupportRepository) MarkAttachmentDeleted(_ context.Context, id, from string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.attachments[id]
	if !ok || a.State != from {
		return false, nil
	}
	a.State = domain.AttachmentDeleted
	return true, nil
}

// fakeAttachmentStore is an in-memory private bucket.
type fakeAttachmentStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	putErr  error
}

func newFakeAttachmentStore() *fakeAttachmentStore {
	return &fakeAttachmentStore{objects: map[string][]byte{}}
}

func (s *fakeAttachmentStore) Put(_ context.Context, key string, data []byte, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.putErr != nil {
		return s.putErr
	}
	s.objects[key] = bytes.Clone(data)
	return nil
}

func (s *fakeAttachmentStore) Open(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[key]
	if !ok {
		return nil, errors.New("no such object")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *fakeAttachmentStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}
