package loopx

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Command is a chat shortcut shared by the IDE, the Orb and the API.
type Command struct {
	Action   string // start, list, stop, pause, resume
	Kind     string
	Text     string
	Max      int
	Checks   []string
	Every    time.Duration
	Cron     string
	Events   []string
	ID       string
	JudgeOff bool
}

// Parse reads /goal and /loop. It returns false when the line is ordinary chat.
func Parse(line string) (Command, bool) {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "/") {
		return Command{}, false
	}
	fields := splitFields(line)
	if len(fields) == 0 {
		return Command{}, false
	}
	switch fields[0] {
	case "/goal":
		return parseGoal(fields[1:])
	case "/loops":
		if len(fields) != 1 {
			return Command{}, false
		}
		return Command{Action: "list"}, true
	case "/loop":
		return parseLoop(fields[1:])
	default:
		return Command{}, false
	}
}

func parseGoal(args []string) (Command, bool) {
	cmd := Command{Action: "start", Kind: KindGoal}
	text, err := fillFlags(&cmd, args)
	if err != nil || strings.TrimSpace(text) == "" {
		return Command{}, false
	}
	cmd.Text = strings.TrimSpace(text)
	return cmd, true
}

func parseLoop(args []string) (Command, bool) {
	if len(args) == 0 {
		return Command{}, false
	}
	switch args[0] {
	case "stop", "pause", "resume":
		if len(args) != 2 || !ValidID(args[1]) {
			return Command{}, false
		}
		return Command{Action: args[0], ID: args[1]}, true
	}
	cmd := Command{Action: "start", Kind: KindTime}
	rest := args
	if args[0] == "on" && len(args) >= 2 {
		cmd.Kind = KindProactive
		cmd.Events = []string{args[1]}
		rest = args[2:]
	} else if d, err := time.ParseDuration(args[0]); err == nil && d > 0 {
		cmd.Every = d
		rest = args[1:]
	} else if args[0] == "cron" && len(args) >= 2 {
		cmd.Cron = args[1]
		rest = args[2:]
	} else {
		return Command{}, false
	}
	text, err := fillFlags(&cmd, rest)
	if err != nil || strings.TrimSpace(text) == "" {
		return Command{}, false
	}
	cmd.Text = strings.TrimSpace(text)
	if cmd.Every > 0 {
		cron, err := DurationCron(cmd.Every)
		if err != nil {
			return Command{}, false
		}
		cmd.Cron = cron
		cmd.Every = roundUpMinute(cmd.Every)
	}
	if strings.TrimSpace(cmd.Cron) == "" && len(cmd.Events) == 0 {
		return Command{}, false
	}
	for _, ev := range cmd.Events {
		switch ev {
		case "arquivo", "lane", "aprovacao", "pr_review", "ci_failed":
		default:
			return Command{}, false
		}
	}
	return cmd, true
}

func fillFlags(cmd *Command, args []string) (string, error) {
	var text []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--max":
			if i+1 >= len(args) {
				return "", fmt.Errorf("max sem valor")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return "", fmt.Errorf("max inválido")
			}
			cmd.Max = n
			i++
		case "--check":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return "", fmt.Errorf("check sem comando")
			}
			cmd.Checks = append(cmd.Checks, args[i+1])
			i++
		case "--no-judge":
			cmd.JudgeOff = true
		default:
			text = append(text, args[i])
		}
	}
	return strings.Join(text, " "), nil
}

// DurationCron converts a /loop interval into the five-field cron the scheduler already runs.
// Intervals shorter than a minute become one minute. Hour multiples become hourly steps.
// Other values above 59 minutes need an explicit cron.
func DurationCron(d time.Duration) (string, error) {
	if d <= 0 {
		return "", fmt.Errorf("intervalo vazio")
	}
	if d < time.Minute {
		d = time.Minute
	}
	if d%time.Hour == 0 {
		h := int(d / time.Hour)
		if h >= 1 && h <= 23 {
			return fmt.Sprintf("0 */%d * * *", h), nil
		}
	}
	mins := int(d / time.Minute)
	if mins < 1 {
		mins = 1
	}
	if mins > 59 {
		return "", fmt.Errorf("intervalo acima de 59 minutos que não é hora cheia precisa de cron")
	}
	return fmt.Sprintf("*/%d * * * *", mins), nil
}

func roundUpMinute(d time.Duration) time.Duration {
	if d < time.Minute {
		return time.Minute
	}
	return d.Truncate(time.Minute)
}

func splitFields(line string) []string {
	var out []string
	var b strings.Builder
	var quote rune
	flush := func() {
		if b.Len() == 0 {
			return
		}
		out = append(out, b.String())
		b.Reset()
	}
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			b.WriteRune(r)
		case r == '"' || r == '\'':
			quote = r
		case unicode.IsSpace(r):
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return out
}
