//go:build windows

package app

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lxn/walk"
)

var logLineBreakReplacer = strings.NewReplacer(
	`\r\n`, "\n",
	`\n`, "\n",
	`\r`, "\n",
	"\r\n", "\n",
	"\r", "\n",
)

func (a *App) pipeLogs(r io.Reader) {
	const maxLogLineBytes = 256 * 1024
	reader := bufio.NewReaderSize(r, 64*1024)
	var lineBuf bytes.Buffer
	truncated := false

	flushLine := func() {
		if lineBuf.Len() == 0 && !truncated {
			return
		}
		line := lineBuf.String()
		if truncated {
			line += " [truncated]"
		}
		a.appendLogLine(line)
		lineBuf.Reset()
		truncated = false
	}

	for {
		part, isPrefix, err := reader.ReadLine()
		if len(part) > 0 {
			if lineBuf.Len() < maxLogLineBytes {
				remaining := maxLogLineBytes - lineBuf.Len()
				if len(part) > remaining {
					_, _ = lineBuf.Write(part[:remaining])
					truncated = true
				} else {
					_, _ = lineBuf.Write(part)
				}
			} else {
				truncated = true
			}
		}
		if !isPrefix {
			flushLine()
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				flushLine()
				return
			}
			a.log("log read error: %v", err)
			return
		}
	}
}

func (a *App) appendLogLine(line string) {
	chunks := normalizeLogChunks(line)
	if len(chunks) == 0 {
		return
	}

	a.logMu.Lock()
	defer a.logMu.Unlock()

	for _, chunk := range chunks {
		a.nextLogID++
		a.appendLogEntryLocked(logEntry{ID: a.nextLogID, Text: chunk})
	}
}

func (a *App) appendLogEntryLocked(entry logEntry) {
	if len(a.logEntries) < maxLogLines {
		a.logEntries = append(a.logEntries, entry)
		return
	}

	if len(a.logEntries) == 0 {
		return
	}

	a.logEntries[a.logStart] = entry
	a.logStart++
	if a.logStart >= len(a.logEntries) {
		a.logStart = 0
	}
}

func normalizeLogChunks(line string) []string {
	line = strings.TrimRight(line, "\r\n \t")
	if line == "" {
		return nil
	}

	line = logLineBreakReplacer.Replace(line)

	raw := strings.Split(line, "\n")
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimRight(part, " \t")
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func (a *App) logsSince(fromID int64) ([]logEntry, int64) {
	a.logMu.Lock()
	defer a.logMu.Unlock()

	if len(a.logEntries) == 0 {
		return nil, a.nextLogID
	}
	if fromID >= a.nextLogID {
		return nil, a.nextLogID
	}

	entries := make([]logEntry, 0, len(a.logEntries))
	for i := 0; i < len(a.logEntries); i++ {
		e := a.logEntries[(a.logStart+i)%len(a.logEntries)]
		if e.ID > fromID {
			entries = append(entries, e)
		}
	}
	return entries, a.nextLogID
}

func (a *App) logsText() string {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	if len(a.logEntries) == 0 {
		return ""
	}
	lines := make([]string, 0, len(a.logEntries))
	for i := 0; i < len(a.logEntries); i++ {
		e := a.logEntries[(a.logStart+i)%len(a.logEntries)]
		lines = append(lines, e.Text)
	}
	return strings.Join(lines, "\r\n")
}

func (a *App) copyLogsToClipboard() error {
	text := a.logsText()
	if text == "" {
		return errors.New("логи пустые")
	}

	var copyErr error
	if a.mw != nil {
		a.mw.Synchronize(func() {
			copyErr = walk.Clipboard().SetText(text)
		})
	} else {
		copyErr = walk.Clipboard().SetText(text)
	}
	if copyErr != nil {
		return copyErr
	}
	a.log("Лог скопирован в буфер обмена")
	return nil
}

func (a *App) log(format string, args ...any) {
	line := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	a.appendLogLine(line)
}
