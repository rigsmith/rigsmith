package appserver

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeServer is the stdio half of an app-server: it reads requests off the
// client's stdin and writes whatever reply is asked for to the client's
// stdout. Nothing here forks a process.
func fakeServer(t *testing.T, serve func(id int, method string, w io.Writer)) *Client {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() {
		sc := bufio.NewScanner(inR)
		sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
		for sc.Scan() {
			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if json.Unmarshal(sc.Bytes(), &req) == nil && req.ID != 0 {
				serve(req.ID, req.Method, outW)
			}
		}
	}()
	t.Cleanup(func() { _ = inW.Close(); _ = outW.Close() })
	return &Client{in: inW, out: bufio.NewReader(outR)}
}

// Two callers on one pipe used to consume each other's replies and discard
// them as "not mine"; the second then waited for an answer already gone by.
func TestConcurrentCallsEachGetTheirOwnReply(t *testing.T) {
	// Serialised, only one request is ever outstanding, so the fake can answer
	// each as it comes. Unserialised, all four arrive before any reply; the
	// fake then answers them in REVERSE, and the first caller's reader takes
	// the fourth's reply, discards it as "not mine", and the fourth waits for
	// an answer that has already gone by.
	var mu sync.Mutex
	var pending []struct {
		id     int
		method string
	}
	const callers = 4
	c := fakeServer(t, func(id int, method string, w io.Writer) {
		mu.Lock()
		defer mu.Unlock()
		pending = append(pending, struct {
			id     int
			method string
		}{id, method})
		flush := len(pending) == callers
		if !flush {
			// Give the other callers a moment to arrive if they are going to;
			// a serialised client sends one at a time and never fills this.
			mu.Unlock()
			time.Sleep(50 * time.Millisecond)
			mu.Lock()
			flush = len(pending) == callers || len(pending) == 1
		}
		if !flush {
			return
		}
		for i := len(pending) - 1; i >= 0; i-- {
			p := pending[i]
			_, _ = io.WriteString(w, `{"id":`+itoa(p.id)+`,"result":{"method":"`+p.method+`"}}`+"\n")
		}
		pending = pending[:0]
	})
	var wg sync.WaitGroup
	for _, m := range []string{"alpha", "beta", "gamma", "delta"} {
		wg.Add(1)
		go func(m string) {
			defer wg.Done()
			res, err := c.Call(context.Background(), m, nil)
			if err != nil {
				t.Errorf("%s: %v", m, err)
				return
			}
			if !strings.Contains(string(res), `"method":"`+m+`"`) {
				t.Errorf("%s got somebody else's reply: %s", m, res)
			}
		}(m)
	}
	wg.Wait()
}

// A server that starts a line and never finishes it used to hold the caller
// forever: the clock was only consulted between complete lines.
func TestAnUnterminatedReplyStillHonoursTheDeadline(t *testing.T) {
	c := fakeServer(t, func(id int, method string, w io.Writer) {
		_, _ = io.WriteString(w, `{"id":`+itoa(id)+`,"result":`) // and then nothing, ever
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.Call(ctx, "hang", nil); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a reply that never ended was accepted")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Call did not return after its deadline")
	}
	// The client is now one message behind its server. A later call must
	// refuse immediately rather than read the stale reply as its own.
	start := time.Now()
	if _, err := c.Call(context.Background(), "again", nil); err == nil || !strings.Contains(err.Error(), "out of step") {
		t.Errorf("a timed-out client accepted another call: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Error("the refusal waited instead of answering at once")
	}
}

func itoa(n int) string {
	return strings.TrimSpace(strings.Repeat(" ", 0) + json.Number(intToString(n)).String())
}

func intToString(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
