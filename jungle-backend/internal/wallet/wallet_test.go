package wallet

import "testing"

func TestNewID(t *testing.T) {
	a, e := NewID()
	if e != nil || len(a) != 36 {
		t.Fatalf("invalid uuid: %q %v", a, e)
	}
	b, _ := NewID()
	if a == b {
		t.Fatal("duplicate uuid")
	}
}
