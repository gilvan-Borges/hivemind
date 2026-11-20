package resident

import (
	"testing"
	"unsafe"
)

func TestHostInputStructIsWindows64(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("layout conferido em 64 bits")
	}
	if unsafe.Sizeof(mouseInput{}) != 40 {
		t.Fatalf("mouseInput = %d, esperado 40", unsafe.Sizeof(mouseInput{}))
	}
	if unsafe.Sizeof(keyInput{}) != 40 {
		t.Fatalf("keyInput = %d, esperado 40", unsafe.Sizeof(keyInput{}))
	}
}
