package session

import "strings"

// Custom runs a user-configured command once per message: the message goes to
// its stdin and its stdout streams back as the reply. It keeps no memory
// between messages beyond what the command itself keeps.
type Custom struct {
	turns   turnRunner
	opts    Options
	onEvent func(Event)
}

// NewCustom creates a conversation with a custom command (opts.Path and
// opts.Args).
func NewCustom(opts Options, onEvent func(Event)) *Custom {
	c := &Custom{opts: opts, onEvent: onEvent}
	c.turns.run = c.turn
	return c
}

func (c *Custom) Start() error {
	c.onEvent(Event{Type: "commands", Commands: []Command{}})
	return nil
}

func (c *Custom) Send(text string, files []string) error {
	c.turns.send(withPaths(text, files))
	return nil
}

// Configure is a no-op: a custom command has no model or effort settings.
func (c *Custom) Configure(model, effort string) {}

func (c *Custom) Close() { c.turns.close() }

func (c *Custom) turn(text string) {
	cmd := c.opts.command(c.opts.Args...)

	c.onEvent(Event{Type: "block"})
	stderr, err := runLines(&c.turns, cmd, text, func(line []byte) {
		c.onEvent(Event{Type: "text", Text: string(line)})
	})
	if c.turns.isClosed() {
		return
	}
	if err != nil {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = err.Error()
		}
		c.onEvent(Event{Type: "done", IsError: true, Text: msg})
		return
	}
	c.onEvent(Event{Type: "done"})
}
