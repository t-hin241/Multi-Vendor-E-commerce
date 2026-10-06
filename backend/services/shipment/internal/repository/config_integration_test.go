package repository_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/repository"
	"shopee/backend/services/shipment/internal/usecase"
)

func TestCarrierRepositoryIntegration(t *testing.T) {
	pool := shipmentDB(t)
	ctx := t.Context()
	repo := repository.NewCarrierRepository(pool)

	beta := &domain.Carrier{Name: "Beta Express", Code: "BETA"}
	alpha := &domain.Carrier{Name: "Alpha Post", Code: "ALPHA"}
	for _, c := range []*domain.Carrier{beta, alpha} {
		if err := repo.Create(ctx, c); err != nil {
			t.Fatal(err)
		}
		if c.ID == "" || !c.IsActive || c.CreatedAt.IsZero() || c.UpdatedAt.IsZero() {
			t.Fatalf("created carrier %+v", c)
		}
	}
	if err := repo.Create(ctx, &domain.Carrier{Name: "Other", Code: "BETA"}); !errors.Is(err, repository.ErrCarrierAlreadyExists) {
		t.Errorf("duplicate code: %v", err)
	}

	if err := repo.SetActive(ctx, beta.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetActive(ctx, uuid.NewString(), false); !errors.Is(err, repository.ErrCarrierNotFound) {
		t.Errorf("SetActive of an unknown carrier: %v", err)
	}
	list, err := repo.List(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("List = %v, %v", list, err)
	}
	if list[0].ID != alpha.ID || list[1].ID != beta.ID || !list[0].IsActive || list[1].IsActive || !list[1].UpdatedAt.After(beta.UpdatedAt) {
		t.Errorf("List by name with flags = %+v, %+v", list[0], list[1])
	}
	if found, err := repo.FindByID(ctx, alpha.ID); err != nil || found.Code != "ALPHA" || found.Name != "Alpha Post" {
		t.Errorf("FindByID = %+v, %v", found, err)
	}
	if _, err := repo.FindByID(ctx, uuid.NewString()); !errors.Is(err, repository.ErrCarrierNotFound) {
		t.Errorf("FindByID of an unknown carrier: %v", err)
	}
}

func TestZoneRepositoryIntegration(t *testing.T) {
	pool := shipmentDB(t)
	ctx := t.Context()
	repo := repository.NewZoneRepository(pool)

	south := &domain.Zone{Name: "South", Code: "S"}
	north := &domain.Zone{Name: "North", Code: "N"}
	for _, z := range []*domain.Zone{south, north} {
		if err := repo.Create(ctx, z); err != nil {
			t.Fatal(err)
		}
		if z.ID == "" || z.CreatedAt.IsZero() {
			t.Fatalf("created zone %+v", z)
		}
	}
	if err := repo.Create(ctx, &domain.Zone{Name: "Other", Code: "N"}); !errors.Is(err, repository.ErrZoneAlreadyExists) {
		t.Errorf("duplicate code: %v", err)
	}
	if list, err := repo.List(ctx); err != nil || len(list) != 2 || list[0].ID != north.ID || list[1].ID != south.ID {
		t.Errorf("List by name = %v, %v", list, err)
	}

	for _, p := range []string{"HN", "HP", "BN"} {
		if err := repo.AddProvince(ctx, north.ID, p); err != nil {
			t.Fatal(err)
		}
	}
	// A province belongs to one zone: mapping it again, anywhere, fails.
	for _, zoneID := range []string{north.ID, south.ID} {
		if err := repo.AddProvince(ctx, zoneID, "HN"); !errors.Is(err, repository.ErrProvinceAlreadyMapped) {
			t.Errorf("remapping HN to %s: %v", zoneID, err)
		}
	}
	if got, err := repo.ListProvinces(ctx, north.ID); err != nil || !slices.Equal(got, []string{"BN", "HN", "HP"}) {
		t.Errorf("ListProvinces = %v, %v", got, err)
	}
	if got, err := repo.ListProvinces(ctx, south.ID); err != nil || len(got) != 0 {
		t.Errorf("ListProvinces of an empty zone = %v, %v", got, err)
	}
	if z, err := repo.FindZoneByProvinceCode(ctx, "HP"); err != nil || z.ID != north.ID {
		t.Errorf("FindZoneByProvinceCode = %+v, %v", z, err)
	}
	if _, err := repo.FindByID(ctx, uuid.NewString()); !errors.Is(err, repository.ErrZoneNotFound) {
		t.Errorf("FindByID of an unknown zone: %v", err)
	}
}

func TestFeeRuleListShowsTheCurrentVersionPerPair(t *testing.T) {
	pool := shipmentDB(t)
	ctx := t.Context()
	carriers, zones, rules := repository.NewCarrierRepository(pool), repository.NewZoneRepository(pool), repository.NewFeeRuleRepository(pool)
	carrier := &domain.Carrier{Name: "Carrier", Code: "C"}
	north, south := &domain.Zone{Name: "North", Code: "N"}, &domain.Zone{Name: "South", Code: "S"}
	if err := carriers.Create(ctx, carrier); err != nil {
		t.Fatal(err)
	}
	for _, z := range []*domain.Zone{north, south} {
		if err := zones.Create(ctx, z); err != nil {
			t.Fatal(err)
		}
	}
	if v, err := rules.CurrentVersion(ctx, carrier.ID, north.ID); err != nil || v != 0 {
		t.Errorf("CurrentVersion without a rule = %d, %v", v, err)
	}
	if rules, err := rules.List(ctx); err != nil || len(rules) != 0 {
		t.Errorf("List without rules = %v, %v", rules, err)
	}
	insert := func(zoneID string, version int, base int64) {
		t.Helper()
		f := &domain.FeeRule{CarrierID: carrier.ID, ZoneID: zoneID, Version: version, BaseFeeAmount: base, FreeWeightGrams: 1000, ExtraFeePerKg: 500}
		if err := rules.Insert(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	insert(north.ID, 1, 10000)
	insert(north.ID, 2, 12000)
	insert(south.ID, 1, 30000)
	// Versions never collide: a second v2 for the pair is refused.
	if err := rules.Insert(ctx, &domain.FeeRule{CarrierID: carrier.ID, ZoneID: north.ID, Version: 2, BaseFeeAmount: 1}); err == nil {
		t.Error("a duplicate version was accepted")
	}

	list, err := rules.List(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("List = %v, %v", list, err)
	}
	current := map[string]*domain.FeeRule{}
	for _, f := range list {
		current[f.ZoneID] = f
	}
	if n := current[north.ID]; n == nil || n.Version != 2 || n.BaseFeeAmount != 12000 || n.FreeWeightGrams != 1000 || n.ExtraFeePerKg != 500 {
		t.Errorf("north current rule = %+v", n)
	}
	if s := current[south.ID]; s == nil || s.Version != 1 || s.BaseFeeAmount != 30000 {
		t.Errorf("south current rule = %+v", s)
	}
	if v, err := rules.CurrentVersion(ctx, carrier.ID, north.ID); err != nil || v != 2 {
		t.Errorf("CurrentVersion = %d, %v", v, err)
	}
}

func TestVendorShippingMethodRepositoryIntegration(t *testing.T) {
	pool := shipmentDB(t)
	ctx := t.Context()
	carriers, repo := repository.NewCarrierRepository(pool), repository.NewVendorShippingMethodRepository(pool)
	a, b := &domain.Carrier{Name: "A", Code: "A"}, &domain.Carrier{Name: "B", Code: "B"}
	for _, c := range []*domain.Carrier{a, b} {
		if err := carriers.Create(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	vendor, other := uuid.NewString(), uuid.NewString()
	first := &domain.VendorShippingMethod{VendorID: vendor, CarrierID: a.ID, IsDefault: true}
	second := &domain.VendorShippingMethod{VendorID: vendor, CarrierID: b.ID}
	for _, m := range []*domain.VendorShippingMethod{first, second} {
		if err := repo.Create(ctx, m); err != nil {
			t.Fatal(err)
		}
		if m.ID == "" || !m.IsActive || m.CreatedAt.IsZero() {
			t.Fatalf("created method %+v", m)
		}
	}
	if err := repo.Create(ctx, &domain.VendorShippingMethod{VendorID: vendor, CarrierID: a.ID}); !errors.Is(err, repository.ErrVendorShippingMethodAlreadyExists) {
		t.Errorf("enabling a carrier twice: %v", err)
	}
	theirs := &domain.VendorShippingMethod{VendorID: other, CarrierID: a.ID, IsDefault: true}
	if err := repo.Create(ctx, theirs); err != nil {
		t.Fatal(err)
	}

	ids := func(vendorID string) []string {
		t.Helper()
		list, err := repo.ListForVendor(ctx, vendorID)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, m := range list {
			out = append(out, m.ID)
		}
		return out
	}
	if got := ids(vendor); !slices.Equal(got, []string{first.ID, second.ID}) {
		t.Errorf("ListForVendor = %v", got)
	}
	if got := ids(uuid.NewString()); len(got) != 0 {
		t.Errorf("ListForVendor of a shop without methods = %v", got)
	}
	if m, err := repo.FindByID(ctx, second.ID); err != nil || m.VendorID != vendor || m.CarrierID != b.ID || m.IsDefault {
		t.Errorf("FindByID = %+v, %v", m, err)
	}
	if _, err := repo.FindByID(ctx, uuid.NewString()); !errors.Is(err, repository.ErrVendorShippingMethodNotFound) {
		t.Errorf("FindByID of an unknown method: %v", err)
	}

	defaultOf := func(vendorID string) string {
		t.Helper()
		m, err := repo.FindDefaultForVendor(ctx, vendorID)
		if errors.Is(err, repository.ErrVendorShippingMethodNotFound) {
			return ""
		}
		if err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	if err := repo.SetDefault(ctx, vendor, second.ID); err != nil {
		t.Fatal(err)
	}
	if got := defaultOf(vendor); got != second.ID {
		t.Errorf("default after SetDefault = %s, want %s", got, second.ID)
	}
	// Another shop's method is not found, and nothing changes.
	if err := repo.SetDefault(ctx, vendor, theirs.ID); !errors.Is(err, repository.ErrVendorShippingMethodNotFound) {
		t.Errorf("SetDefault of another shop's method: %v", err)
	}
	if defaultOf(vendor) != second.ID || defaultOf(other) != theirs.ID {
		t.Error("a refused SetDefault changed a default")
	}
	if err := repo.SetActive(ctx, vendor, theirs.ID, false); !errors.Is(err, repository.ErrVendorShippingMethodNotFound) {
		t.Errorf("SetActive of another shop's method: %v", err)
	}
	// An inactive default is no default for quoting.
	if err := repo.SetActive(ctx, vendor, second.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := defaultOf(vendor); got != "" {
		t.Errorf("inactive default still used: %s", got)
	}
	if m, err := repo.FindByID(ctx, second.ID); err != nil || m.IsActive || !m.IsDefault {
		t.Errorf("after SetActive(false) = %+v, %v", m, err)
	}
}

// The repository joins the caller's transaction: a rolled-back use case
// leaves no method behind and no default moved.
func TestVendorShippingMethodsJoinTheCallersTransaction(t *testing.T) {
	pool := shipmentDB(t)
	ctx := t.Context()
	carriers, repo := repository.NewCarrierRepository(pool), repository.NewVendorShippingMethodRepository(pool)
	a, b := &domain.Carrier{Name: "A", Code: "A"}, &domain.Carrier{Name: "B", Code: "B"}
	for _, c := range []*domain.Carrier{a, b} {
		if err := carriers.Create(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	vendor := uuid.NewString()
	first := &domain.VendorShippingMethod{VendorID: vendor, CarrierID: a.ID, IsDefault: true}
	if err := repo.Create(ctx, first); err != nil {
		t.Fatal(err)
	}

	rollback := errors.New("use case failed")
	err := repository.Transactions{Pool: pool}.Run(ctx, func(ctx context.Context) error {
		second := &domain.VendorShippingMethod{VendorID: vendor, CarrierID: b.ID}
		if err := repo.Create(ctx, second); err != nil {
			return err
		}
		if err := repo.SetDefault(ctx, vendor, second.ID); err != nil {
			return err
		}
		if err := repo.SetActive(ctx, vendor, first.ID, false); err != nil {
			return err
		}
		if m, err := repo.FindDefaultForVendor(ctx, vendor); err != nil || m.ID != second.ID {
			t.Errorf("inside the transaction the new default is visible: %+v %v", m, err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	list, err := repo.ListForVendor(ctx, vendor)
	if err != nil || len(list) != 1 || list[0].ID != first.ID || !list[0].IsDefault || !list[0].IsActive {
		t.Errorf("after rollback = %+v, %v", list, err)
	}
}

// A new shop enabling two carriers at once gets both, exactly one of them
// as its default, and never a misleading "already enabled" error.
func TestConcurrentFirstEnablesPickOneDefault(t *testing.T) {
	pool := shipmentDB(t)
	ctx := t.Context()
	carriers := repository.NewCarrierRepository(pool)
	methods := repository.NewVendorShippingMethodRepository(pool)
	user := uuid.NewString()
	for round := 0; round < 10; round++ {
		vendor := uuid.NewString()
		uc := usecase.NewVendorShippingMethodUseCase(methods, carriers, fakeVendors{owners: map[string]string{user: vendor}})
		ids := make([]string, 4)
		for i := range ids {
			c := &domain.Carrier{Name: "Carrier", Code: uuid.NewString()[:8]}
			if err := carriers.Create(ctx, c); err != nil {
				t.Fatal(err)
			}
			ids[i] = c.ID
		}
		var wg sync.WaitGroup
		errs := make([]error, len(ids))
		for i, carrierID := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = uc.Enable(context.Background(), user, vendor, carrierID)
			}()
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				var app *apperror.Error
				errors.As(err, &app)
				t.Fatalf("round %d: enable %d: %v (%+v)", round, i, err, app)
			}
		}
		list, err := methods.ListForVendor(ctx, vendor)
		if err != nil {
			t.Fatal(err)
		}
		defaults := 0
		for _, m := range list {
			if m.IsDefault {
				defaults++
			}
		}
		if len(list) != len(ids) || defaults != 1 {
			t.Fatalf("round %d: %d methods, %d defaults", round, len(list), defaults)
		}
	}
}
