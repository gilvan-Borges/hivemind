package loopx

import (
	"testing"
	"time"
)

func TestParseGoalAndLoop(t *testing.T) {
	goal, ok := Parse(`/goal testes de auth verdes --max 5 --check "go test ./internal/auth"`)
	if !ok || goal.Kind != KindGoal || goal.Max != 5 || len(goal.Checks) != 1 || goal.Text != "testes de auth verdes" {
		t.Fatalf("goal: %+v %v", goal, ok)
	}
	if _, ok := Parse("olá"); ok {
		t.Fatal("chat comum não é comando")
	}
	list, ok := Parse("/loops")
	if !ok || list.Action != "list" {
		t.Fatalf("lista: %+v", list)
	}
	stop, ok := Parse("/loop stop fix-auth")
	if !ok || stop.Action != "stop" || stop.ID != "fix-auth" {
		t.Fatalf("stop: %+v", stop)
	}
	timed, ok := Parse("/loop 10m cuidar do CI --max 3")
	if !ok || timed.Kind != KindTime || timed.Cron != "*/10 * * * *" || timed.Max != 3 || timed.Text != "cuidar do CI" {
		t.Fatalf("tempo: %+v", timed)
	}
	hour, err := DurationCron(2 * time.Hour)
	if err != nil || hour != "0 */2 * * *" {
		t.Fatalf("hora: %s %v", hour, err)
	}
}
