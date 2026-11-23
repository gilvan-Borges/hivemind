package loopx

import "testing"

func TestEffectiveFullUnattended(t *testing.T) {
	if EffectiveFull(false, true, true) {
		t.Fatal("projeto governado não vira Full")
	}
	if !EffectiveFull(true, true, false) {
		t.Fatal("acompanhado segue o Full do projeto")
	}
	if EffectiveFull(true, false, false) {
		t.Fatal("cron em Full sem o interruptor deve continuar governado")
	}
	if !EffectiveFull(true, false, true) {
		t.Fatal("interruptor local libera o Full desacompanhado")
	}
}

func TestApplyPermissionNeverEscalates(t *testing.T) {
	if ApplyPermission("ask", true, true, true) {
		t.Fatal("ask não pode herdar Full")
	}
	if ApplyPermission("full", true, true, true) {
		t.Fatal("a spec não liga Full")
	}
	if !ApplyPermission("inherit", true, true, false) {
		t.Fatal("inherit acompanhado usa o Full do projeto")
	}
	if ApplyPermission("inherit", true, false, false) {
		t.Fatal("inherit desacompanhado sem interruptor fica governado")
	}
}
