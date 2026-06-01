package store

import "testing"

func TestInMemoryTransferRequiresFullBalanceAndFloorVariantClamps(t *testing.T) {
	st := NewInMemory()
	if _, err := st.Recharge(t.Context(), "alice", 10); err != nil {
		t.Fatalf("seed alice: %v", err)
	}

	if _, err := st.Transfer(t.Context(), "alice", "bob", 11); err != ErrInsufficientBalance {
		t.Fatalf("transfer insufficient err=%v want %v", err, ErrInsufficientBalance)
	}
	accounts, err := st.ListTokenAccounts(t.Context())
	if err != nil {
		t.Fatalf("list accounts after failed transfer: %v", err)
	}
	bal := map[string]int64{}
	for _, item := range accounts {
		bal[item.BotID] = item.Balance
	}
	if bal["alice"] != 10 {
		t.Fatalf("alice balance=%d want 10", bal["alice"])
	}
	if bal["bob"] != 0 {
		t.Fatalf("bob balance=%d want 0", bal["bob"])
	}

	transfer, err := st.TransferWithFloor(t.Context(), "alice", "bob", 11)
	if err != nil {
		t.Fatalf("transfer with floor: %v", err)
	}
	if transfer.Deducted != 10 {
		t.Fatalf("deducted=%d want 10", transfer.Deducted)
	}
	accounts, err = st.ListTokenAccounts(t.Context())
	if err != nil {
		t.Fatalf("list accounts after floor transfer: %v", err)
	}
	bal = map[string]int64{}
	for _, item := range accounts {
		bal[item.BotID] = item.Balance
	}
	if bal["alice"] != 0 {
		t.Fatalf("alice balance=%d want 0", bal["alice"])
	}
	if bal["bob"] != 10 {
		t.Fatalf("bob balance=%d want 10", bal["bob"])
	}
}
