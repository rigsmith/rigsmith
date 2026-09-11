package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

const routingSettingsLimit = 1 << 20

// Queue routing only reads bounded regular settings files. Cancellation is
// cooperative between filesystem operations and read chunks; a kernel call on
// a stalled filesystem cannot be interrupted by context cancellation.
func loadRoutingSettings(ctx context.Context, path string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > routingSettingsLimit {
		return nil, fmt.Errorf("queued hook settings must be a regular file no larger than 1 MiB")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Size() > routingSettingsLimit {
		return nil, fmt.Errorf("queued hook settings changed during open")
	}
	return readRoutingSettings(ctx, f)
}

func readRoutingSettings(ctx context.Context, r io.Reader) (map[string]any, error) {
	data, err := io.ReadAll(io.LimitReader(routingSettingsReader{ctx, r}, routingSettingsLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > routingSettingsLimit {
		return nil, fmt.Errorf("queued hook settings exceed 1 MiB")
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil || settings == nil {
		return nil, fmt.Errorf("queued hook settings must be a JSON object")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return settings, nil
}

type routingSettingsReader struct {
	ctx context.Context
	r   io.Reader
}

func (r routingSettingsReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > 32<<10 {
		p = p[:32<<10]
	}
	n, err := r.r.Read(p)
	if canceled := r.ctx.Err(); canceled != nil {
		return n, canceled
	}
	return n, err
}
