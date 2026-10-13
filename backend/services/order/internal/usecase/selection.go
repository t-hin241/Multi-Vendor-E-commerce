package usecase

import "context"

type SelectionInput struct {
	CartVersion     *int64
	SelectedLineIDs []string
}

// SelectiveCheckoutCapability fails closed until both consumers are deployed.
func (uc *OrderUseCase) SelectiveCheckoutCapability(ctx context.Context, buyerID string) (bool, error) {
	if !uc.SelectiveCheckoutEnabled {
		return false, nil
	}
	snapshot, err := uc.Cart.Lines(ctx, buyerID)
	if err != nil {
		return false, err
	}
	return snapshot.SelectiveCheckoutEnabled, nil
}
