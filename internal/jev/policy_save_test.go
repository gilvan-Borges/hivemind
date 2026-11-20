package jev

import "testing"

func TestValidatePolicyRejectsBadWeightsAndClasses(t *testing.T) {
	pol := DefaultPolicy()
	pol.Weights = Weights{Quality: -1, Cost: 1, Latency: 0}
	if err := ValidatePolicy(pol); err == nil {
		t.Fatal("peso negativo deveria falhar")
	}
	pol = DefaultPolicy()
	pol.AllowDataClass = []DataClass{"segredo"}
	if err := ValidatePolicy(pol); err == nil {
		t.Fatal("classe desconhecida deveria falhar")
	}
	pol = DefaultPolicy()
	if err := ValidatePolicy(pol); err != nil {
		t.Fatal(err)
	}
}
