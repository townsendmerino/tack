package core

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"

	"github.com/townsendmerino/tack/internal/proto"
)

// ErrClosed is a call on a connection that has closed: the other end went away, or writing to it failed.
var ErrClosed = errors.New("core: connection closed")

// Client speaks the protocol to a Core (in-process over a pipe, or a daemon over its socket). Calls may be made from
// any goroutine; session/event notifications go to OnEvent, called from the reading goroutine in arrival order.
type Client struct {
	mu      sync.Mutex
	enc     *json.Encoder
	next    int
	pending map[string]chan proto.Response
	closed  error
	done    chan struct{} // closed when the connection is

	OnEvent func(proto.SessionEventParams)
}

// NewClient starts reading rw. Set OnEvent before the first call that can produce events.
func NewClient(rw io.ReadWriter) *Client {
	c := &Client{enc: json.NewEncoder(rw), pending: map[string]chan proto.Response{}, done: make(chan struct{})}
	go c.read(rw)
	return c
}

func (c *Client) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var probe struct {
			Method string          `json:"method"`
			ID     json.RawMessage `json:"id"`
		}
		if json.Unmarshal(sc.Bytes(), &probe) != nil {
			continue
		}
		if probe.Method == proto.MethodSessionEvent {
			var req proto.Request
			var p proto.SessionEventParams
			if json.Unmarshal(sc.Bytes(), &req) == nil && json.Unmarshal(req.Params, &p) == nil && c.OnEvent != nil {
				c.OnEvent(p)
			}
			continue
		}
		var resp proto.Response
		if json.Unmarshal(sc.Bytes(), &resp) != nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[string(resp.ID)]
		delete(c.pending, string(resp.ID))
		c.mu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
	c.mu.Lock()
	c.closed = ErrClosed
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	close(c.done)
}

// Done is closed once the connection has closed (the other end went away, or the read failed), after the last event
// it carried has gone to OnEvent.
func (c *Client) Done() <-chan struct{} { return c.done }

// Call sends a request and decodes its result into result (which may be nil).
func (c *Client) Call(method string, params, result any) error {
	p, err := json.Marshal(params)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed != nil {
		c.mu.Unlock()
		return c.closed
	}
	c.next++
	id := json.RawMessage(strconv.Itoa(c.next))
	ch := make(chan proto.Response, 1)
	c.pending[string(id)] = ch
	err = c.enc.Encode(proto.Request{JSONRPC: "2.0", ID: id, Method: method, Params: p})
	if err != nil {
		delete(c.pending, string(id))
	}
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrClosed, err)
	}
	resp, ok := <-ch
	if !ok {
		return ErrClosed
	}
	if resp.Error != nil {
		return fmt.Errorf("%s: %w", method, resp.Error)
	}
	if result != nil {
		return json.Unmarshal(resp.Result, result)
	}
	return nil
}
