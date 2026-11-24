package main

import "testing"

func TestClampOrbExpandedKeepsAUsableBox(t *testing.T) {
	w, h := clampOrbExpanded(100, 100, 1920, 1080)
	if w != orbMinExpandedW || h != orbMinExpandedH {
		t.Fatalf("mínimo: %d×%d", w, h)
	}
	w, h = clampOrbExpanded(4000, 4000, 1280, 800)
	if w != 1264 || h != 784 {
		t.Fatalf("máximo da tela: %d×%d", w, h)
	}
}
