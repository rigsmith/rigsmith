package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type queueHookPayload struct {
	Event, SessionID, TranscriptPath string
}

// Hook input is untrusted vendor data, never a saved queue request. Decode only
// routing fields; do not persist message text, supplied identity or event IDs.
// Unlike ordinary sync's legacy reader, require a complete bounded document.
func readQueueHook(ctx context.Context, in io.Reader, wait time.Duration) (queueHookPayload, error) {
	data, err := readQueueHookInput(ctx, in, wait)
	if err != nil {
		return queueHookPayload{}, err
	}
	return decodeQueueHook(data)
}

func readQueueHookInput(ctx context.Context, in io.Reader, wait time.Duration) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type result struct {
		data []byte
		err  error
	}
	// Memory readers finish synchronously. Only native files and io.PipeReader
	// support the asynchronous path: their Close interrupts a pending read.
	// Reject arbitrary readers before starting work rather than leaking a blocked
	// goroutine (an io.Closer interface alone does not promise interruption).
	var closeInput func() error
	switch reader := in.(type) {
	case *bytes.Reader, *bytes.Buffer, *strings.Reader:
	case *os.File:
		closeInput = reader.Close
	case *io.PipeReader:
		closeInput = reader.Close
	default:
		return nil, fmt.Errorf("hook input requires a memory reader, native file or interruptible pipe")
	}
	read := func() result {
		data, err := io.ReadAll(io.LimitReader(in, queueRequestLimit+1))
		return result{data, err}
	}
	var got result
	if closeInput == nil {
		got = read()
	} else {
		ready := make(chan result, 1)
		go func() { ready <- read() }()
		timer := time.NewTimer(wait)
		defer timer.Stop()
		stopRead := func() {
			_ = closeInput()
			<-ready // account for termination of the read before returning
		}
		select {
		case <-ctx.Done():
			stopRead()
			return nil, ctx.Err()
		case <-timer.C:
			stopRead()
			return nil, fmt.Errorf("hook input did not finish within its read deadline")
		case got = <-ready:
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if got.err != nil {
		return nil, fmt.Errorf("could not read hook input")
	}
	if len(got.data) > queueRequestLimit {
		return nil, fmt.Errorf("hook input exceeds 128 KiB")
	}
	return got.data, nil
}

func decodeQueueHook(data []byte) (queueHookPayload, error) {
	fail := func() (queueHookPayload, error) {
		// Never include raw payloads or JSON decoder errors in hook diagnostics.
		return queueHookPayload{}, fmt.Errorf("hook input must be one unambiguous Stop/SessionEnd JSON object with session_id and transcript_path")
	}
	if !utf8.Valid(data) {
		return fail()
	}
	d := json.NewDecoder(bytes.NewReader(data))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return fail()
	}
	fields := map[string]json.RawMessage{}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		folded := strings.ToLower(key)
		if err != nil || !ok {
			return fail()
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return fail()
		}
		switch folded {
		case "hook_event_name", "session_id", "transcript_path", "agent_id":
			if key != folded || seen[folded] {
				return fail()
			}
			seen[folded] = true
			fields[key] = value
		}
	}
	end, err := d.Token()
	if err != nil || end != json.Delim('}') {
		return fail()
	}
	if _, err := d.Token(); err != io.EOF {
		return fail()
	}
	var p queueHookPayload
	for key, target := range map[string]*string{"hook_event_name": &p.Event, "session_id": &p.SessionID, "transcript_path": &p.TranscriptPath} {
		if err := json.Unmarshal(fields[key], target); err != nil || strings.TrimSpace(*target) == "" || strings.ContainsRune(*target, utf8.RuneError) || strings.IndexFunc(*target, unicode.IsControl) >= 0 {
			return fail()
		}
	}
	if p.Event != "Stop" && p.Event != "SessionEnd" {
		return fail()
	}
	if raw, ok := fields["agent_id"]; ok {
		var agent *string
		if err := json.Unmarshal(raw, &agent); err != nil || agent == nil || *agent != "" {
			return fail()
		}
	}
	return p, nil
}
