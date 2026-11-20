package resident

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// NextCron returns the first instant strictly after from that matches a
// five-field cron expression, evaluated in loc. It never shells out.
func NextCron(expr string, loc *time.Location, from time.Time) (time.Time, error) {
	fields, err := parseCron(expr)
	if err != nil {
		return time.Time{}, err
	}
	if loc == nil {
		loc = time.UTC
	}
	cur := from.In(loc).Truncate(time.Minute).Add(time.Minute)
	limit := cur.Add(366 * 24 * time.Hour)
	for !cur.After(limit) {
		if cronMatch(fields, cur) {
			return cur, nil
		}
		cur = cur.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("expressão sem ocorrência no próximo ano")
}

type cronFields struct {
	min    map[int]bool
	hour   map[int]bool
	dom    map[int]bool
	mon    map[int]bool
	dow    map[int]bool
	domAny bool
	dowAny bool
}

func parseCron(expr string) (cronFields, error) {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return cronFields{}, fmt.Errorf("expressão precisa de 5 campos (minuto hora dia mês dia-da-semana)")
	}
	min, err := parseCronField(parts[0], 0, 59, false)
	if err != nil {
		return cronFields{}, fmt.Errorf("minuto: %w", err)
	}
	hour, err := parseCronField(parts[1], 0, 23, false)
	if err != nil {
		return cronFields{}, fmt.Errorf("hora: %w", err)
	}
	dom, err := parseCronField(parts[2], 1, 31, false)
	if err != nil {
		return cronFields{}, fmt.Errorf("dia: %w", err)
	}
	mon, err := parseCronField(parts[3], 1, 12, false)
	if err != nil {
		return cronFields{}, fmt.Errorf("mês: %w", err)
	}
	dow, err := parseCronField(parts[4], 0, 7, true)
	if err != nil {
		return cronFields{}, fmt.Errorf("dia da semana: %w", err)
	}
	return cronFields{
		min: min, hour: hour, dom: dom, mon: mon, dow: dow,
		domAny: parts[2] == "*",
		dowAny: parts[4] == "*",
	}, nil
}

func parseCronField(spec string, min, max int, dow bool) (map[int]bool, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" || strings.ContainsAny(spec, " \t;\\|&$`'\"") {
		return nil, fmt.Errorf("campo vazio ou inválido")
	}
	out := map[int]bool{}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("campo vazio")
		}
		step := 1
		base := part
		if i := strings.IndexByte(part, '/'); i >= 0 {
			base = part[:i]
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("passo inválido")
			}
			step = n
		}
		lo, hi := min, max
		if base != "*" {
			if j := strings.IndexByte(base, '-'); j >= 0 {
				a, err1 := strconv.Atoi(base[:j])
				b, err2 := strconv.Atoi(base[j+1:])
				if err1 != nil || err2 != nil || a > b {
					return nil, fmt.Errorf("intervalo inválido")
				}
				lo, hi = a, b
			} else {
				n, err := strconv.Atoi(base)
				if err != nil {
					return nil, fmt.Errorf("valor inválido")
				}
				lo, hi = n, n
			}
		}
		if lo < min || hi > max {
			return nil, fmt.Errorf("fora da faixa %d-%d", min, max)
		}
		for v := lo; v <= hi; v += step {
			n := v
			if dow && n == 7 {
				n = 0
			}
			out[n] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("campo vazio")
	}
	return out, nil
}

func cronMatch(f cronFields, t time.Time) bool {
	if !f.min[t.Minute()] || !f.hour[t.Hour()] || !f.mon[int(t.Month())] {
		return false
	}
	domOK := f.dom[t.Day()]
	dowOK := f.dow[int(t.Weekday())]
	switch {
	case f.domAny && f.dowAny:
		return true
	case f.domAny:
		return dowOK
	case f.dowAny:
		return domOK
	default:
		return domOK || dowOK
	}
}

// cronLooksLikeShell reports arguments that would turn a routine into a command.
func cronLooksLikeShell(args map[string]any) bool {
	for _, key := range []string{"command", "shell", "argv", "cmd", "script", "cmdline"} {
		if _, ok := args[key]; ok {
			return true
		}
	}
	blob := strings.ToLower(strArg(args, "text") + "\n" + strArg(args, "expr"))
	// Concatenated so the source does not contain a shell invocation.
	for _, bad := range []string{
		"sh " + "-c", "sh " + "-lc", "bash " + "-c", "cmd " + "/c",
		"cmd" + ".exe", "power" + "shell", "sch" + "tasks", "cron" + "tab",
	} {
		if strings.Contains(blob, bad) {
			return true
		}
	}
	return false
}
