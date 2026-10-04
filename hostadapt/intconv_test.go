package hostadapt

import (
	"math"
	"strings"
	"testing"
)

func TestCheckedGoInt(t *testing.T) {
	got, err := CheckedGoInt[int8](127)
	if err != nil {
		t.Fatalf("CheckedGoInt[int8](127): %v", err)
	}
	if got != 127 {
		t.Fatalf("CheckedGoInt[int8](127) = %d", got)
	}

	if _, err := CheckedGoInt[int8](128); err == nil || !strings.Contains(err.Error(), "overflows Go int8") {
		t.Fatalf("CheckedGoInt[int8](128) error = %v", err)
	}

	u, err := CheckedGoInt[uint32](42)
	if err != nil {
		t.Fatalf("CheckedGoInt[uint32](42): %v", err)
	}
	if u != 42 {
		t.Fatalf("CheckedGoInt[uint32](42) = %d", u)
	}

	if _, err := CheckedGoInt[uint32](-1); err == nil || !strings.Contains(err.Error(), "overflows Go uint32") {
		t.Fatalf("CheckedGoInt[uint32](-1) error = %v", err)
	}
}

func TestMustGoIntPanicsOnOverflow(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		if !strings.Contains(r.(error).Error(), "overflows Go int16") {
			t.Fatalf("panic = %v", r)
		}
	}()
	_ = MustGoInt[int16](math.MaxInt64)
}

func TestCheckedNomiInt(t *testing.T) {
	got, err := CheckedNomiInt(int16(-7))
	if err != nil {
		t.Fatalf("CheckedNomiInt(int16(-7)): %v", err)
	}
	if got != -7 {
		t.Fatalf("CheckedNomiInt(int16(-7)) = %d", got)
	}

	u, err := CheckedNomiInt(uint32(99))
	if err != nil {
		t.Fatalf("CheckedNomiInt(uint32(99)): %v", err)
	}
	if u != 99 {
		t.Fatalf("CheckedNomiInt(uint32(99)) = %d", u)
	}

	if _, err := CheckedNomiInt(uint64(math.MaxUint64)); err == nil || !strings.Contains(err.Error(), "overflows Nomi Int") {
		t.Fatalf("CheckedNomiInt(MaxUint64) error = %v", err)
	}
}
