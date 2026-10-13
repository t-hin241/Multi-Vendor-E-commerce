package usecase

import (
	"shopee/backend/pkg/shopaccess"
	"testing"
)

func TestInventoryNoticeRequiresAdjustmentPermission(t *testing.T) {
	if recipientPurposes["inventory"] != shopaccess.InventoryAdjust {
		t.Fatal("stock notices must use inventory.adjust grants")
	}
}
