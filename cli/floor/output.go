package floor

import (
	"fmt"
	"io"
	"os"
	"regexp"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// Output handles all floor output to terminal and optional log file.
// All visible output should go through Print(). ANSI codes are
// automatically stripped when writing to the log file.
// Use Terminal() for ephemeral terminal-only output (spinners, line clearing).
type Output struct {
	debug   bool
	logFile *os.File
}

// NewOutput creates an Output. If logPath is non-empty, a log file is opened.
func NewOutput(logPath string, debug bool) *Output {
	o := &Output{debug: debug}
	if logPath != "" {
		lf, err := os.Create(logPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: cannot open log file %s: %v\n", logPath, err)
		} else {
			o.logFile = lf
		}
	}
	return o
}

// Print writes to both terminal (with ANSI) and log file (ANSI stripped).
func (o *Output) Print(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	fmt.Print(s)
	o.writeLog(s)
}

// Debug writes a debug line in grey to terminal and plain to log.
// No-op if debug mode is disabled.
func (o *Output) Debug(format string, args ...any) {
	if !o.debug {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("  %s[debug] %s%s\n", Gray, msg, Reset)
	o.writeLog(fmt.Sprintf("  [debug] %s\n", msg))
}

// Terminal writes only to the terminal. Use for ephemeral output
// like "thinking..." spinners and \r\033[K line clearing.
func (o *Output) Terminal(format string, args ...any) {
	fmt.Printf(format, args...)
}

// AgentLabel prints a colored agent label.
func (o *Output) AgentLabel(id string, color string) {
	o.Print("%s%s[%s]:%s ", Bold, color, id, Reset)
}

// LogWriter returns an io.Writer for the log file, or nil if no log is open.
// Used to pass the log to subsystems (e.g. ACP client).
func (o *Output) LogWriter() io.Writer {
	if o.logFile != nil {
		return o.logFile
	}
	return nil
}

// Close closes the log file if open.
func (o *Output) Close() {
	if o.logFile != nil {
		o.logFile.Close()
		o.logFile = nil
	}
}

// Log writes plain text directly to the log file only (no terminal output).
// Use for content that already appeared on terminal through other means
// (e.g. user-typed input from readline).
func (o *Output) Log(format string, args ...any) {
	o.writeLog(fmt.Sprintf(format, args...))
}

// writeLog writes plain text (ANSI stripped) to the log file, if open.
func (o *Output) writeLog(s string) {
	if o.logFile != nil {
		fmt.Fprint(o.logFile, ansiRe.ReplaceAllString(s, ""))
	}
}
