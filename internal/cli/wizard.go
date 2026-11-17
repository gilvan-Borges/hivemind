package cli

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type asker struct {
	in  *bufio.Reader
	out io.Writer
}

func newAsker(in io.Reader, out io.Writer) *asker {
	return &asker{in: bufio.NewReader(in), out: out}
}

func (a *asker) text(prompt, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(a.out, "%s [%s]: ", prompt, def)
	} else {
		fmt.Fprintf(a.out, "%s: ", prompt)
	}
	line, err := a.in.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return line, nil
}

func (a *asker) yesNo(prompt string, def bool) (bool, error) {
	shown := "n"
	if def {
		shown = "s"
	}
	answer, err := a.text(prompt+" (s/n)", shown)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(answer) {
	case "s", "sim", "y", "yes":
		return true, nil
	case "n", "nao", "não", "no":
		return false, nil
	default:
		return false, fmt.Errorf("responda s ou n")
	}
}

func (a *asker) integer(prompt string, def int) (int, error) {
	answer, err := a.text(prompt, strconv.Itoa(def))
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(answer)
	if err != nil {
		return 0, fmt.Errorf("%s precisa ser um número", prompt)
	}
	return n, nil
}
