package frontend

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ergochat/readline"
)

// lineReader reads one line of user input after showing a prompt.
type lineReader interface {
	// ReadLine shows prompt and returns the line without its newline.
	// io.EOF means input has ended (Ctrl-D, Ctrl-C, or end of a pipe).
	ReadLine(prompt string) (string, error)
	Close() error
}

// newLineReader reads from stdin. On a terminal, lines can be edited
// (arrow keys, emacs keys, Ctrl-R) and earlier lines are recalled from
// historyFile, if given. Otherwise lines are read as they come, so piped
// input works as before.
func newLineReader(historyFile string) lineReader {
	if info, err := os.Stdin.Stat(); err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return &plainLines{in: bufio.NewReader(os.Stdin)}
	}
	rl, err := readline.NewFromConfig(&readline.Config{HistoryFile: historyFile})
	if err != nil {
		return &plainLines{in: bufio.NewReader(os.Stdin)}
	}
	return &editedLines{rl: rl}
}

// plainLines prints the prompt and reads up to a newline.
type plainLines struct{ in *bufio.Reader }

func (p *plainLines) ReadLine(prompt string) (string, error) {
	fmt.Print(prompt)
	line, err := p.in.ReadString('\n')
	if err != nil && (err != io.EOF || line == "") {
		return "", io.EOF
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (p *plainLines) Close() error { return nil }

// editedLines is a readline instance on the terminal.
type editedLines struct{ rl *readline.Instance }

func (e *editedLines) ReadLine(prompt string) (string, error) {
	e.rl.SetPrompt(prompt)
	line, err := e.rl.ReadLine()
	if err != nil {
		return "", io.EOF
	}
	return line, nil
}

func (e *editedLines) Close() error { return e.rl.Close() }
