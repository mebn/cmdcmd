package session

import (
	"bufio"
	"bytes"
	"os/exec"
	"strings"
	"sync"
)

// turnRunner runs one process per message, one at a time, for agents that
// have no long-lived mode. Messages sent while a turn runs wait their turn.
type turnRunner struct {
	mu      sync.Mutex
	queue   []string
	running *exec.Cmd
	closed  bool

	// run executes one turn and blocks until it finishes.
	run func(text string)
}

func (t *turnRunner) send(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.queue = append(t.queue, text)
	if len(t.queue) == 1 {
		go t.loop()
	}
}

func (t *turnRunner) loop() {
	for {
		t.mu.Lock()
		if t.closed || len(t.queue) == 0 {
			t.mu.Unlock()
			return
		}
		text := t.queue[0]
		t.mu.Unlock()

		t.run(text)

		t.mu.Lock()
		if len(t.queue) > 0 {
			t.queue = t.queue[1:]
		}
		t.mu.Unlock()
	}
}

// track records the running process so close can stop it. It reports false
// if the runner is already closed.
func (t *turnRunner) track(cmd *exec.Cmd) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.running = cmd
	return !t.closed
}

func (t *turnRunner) isClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func (t *turnRunner) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	t.queue = nil
	if t.running != nil && t.running.Process != nil {
		t.running.Process.Kill()
	}
}

// runLines starts cmd and calls onLine for each stdout line. It returns the
// tail of stderr and the process error.
func runLines(t *turnRunner, cmd *exec.Cmd, stdin string, onLine func([]byte)) (string, error) {
	hideWindow(cmd)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{buf: &stderr, max: 8 << 10}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	if !t.track(cmd) {
		cmd.Process.Kill()
	}
	r := bufio.NewReaderSize(stdout, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			onLine(line)
		}
		if err != nil {
			break
		}
	}
	err = cmd.Wait()
	return strings.TrimSpace(stderr.String()), err
}
