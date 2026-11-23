package loopx

import "testing"

func TestPassesOnlyAndFeaturePick(t *testing.T) {
	before := []byte(`[
	  {"id":"a","title":"A","priority":2,"passes":false,"steps":["s"]},
	  {"id":"b","title":"B","priority":1,"passes":false,"steps":["t"]}
	]`)
	after := []byte(`[
	  {"id":"a","title":"A","priority":2,"passes":true,"steps":["s"]},
	  {"id":"b","title":"B","priority":1,"passes":false,"steps":["t"]}
	]`)
	if !PassesOnlyChange(before, after) {
		t.Fatal("só passes deveria passar")
	}
	cheat := []byte(`[
	  {"id":"a","title":"A","priority":2,"passes":true,"steps":[]},
	  {"id":"b","title":"B","priority":1,"passes":false,"steps":["t"]}
	]`)
	if PassesOnlyChange(before, cheat) {
		t.Fatal("apagar step é trapaça")
	}
	items, err := ParseFeatureBlock("segue\n```json\n" + string(before) + "\n```")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Passes {
			t.Fatal("o parser zera passes")
		}
	}
	next, ok := NextFeature(items)
	if !ok || next.ID != "b" {
		t.Fatalf("próximo: %+v %v", next, ok)
	}
	if AllPass(MarkPass(items, "a", true)) {
		t.Fatal("ainda falta b")
	}
	if !AllPass(MarkPass(MarkPass(items, "a", true), "b", true)) {
		t.Fatal("os dois passaram")
	}
}
