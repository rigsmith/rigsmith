package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

var errPrivateQueueStateLimit = errors.New("private queue state exceeds its byte limit")

// Callers clear their own checksum field in a typed value before hashing. Keep
// field order and representation stable: these bytes are a persisted contract.
func privateQueueStateChecksum(value any) string {
	data, _ := json.Marshal(value)
	return artifact.Key(data)
}

// readPrivateQueueState provides the common file and encoding checks. Callers
// retain ownership of destination selection, directory validation, schema,
// checksum and lifecycle rules; this helper never chooses or creates a path.
func readPrivateQueueState(path string, limit int64, label string, target any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || !queueInboxPrivate(info) || info.Size() > limit {
		return fmt.Errorf("invalid %s private file (maximum %d bytes)", label, limit)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, opened) {
		return fmt.Errorf("%s changed during read", label)
	}
	if err := validateQueueRequestSingleLink(f); err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return errPrivateQueueStateLimit
	}
	if json.Unmarshal(data, target) != nil {
		return fmt.Errorf("invalid %s JSON", label)
	}
	canonical, err := json.Marshal(target)
	// Re-encoding rejects extra/aliased/duplicate fields, null scalars, invalid
	// Unicode and noncanonical whitespace without echoing untrusted contents.
	if err != nil || !bytes.Equal(append(canonical, '\n'), data) {
		return fmt.Errorf("noncanonical %s JSON", label)
	}
	return nil
}

func writePrivateQueueState(ctx context.Context, path string, value any, limit int, save func(context.Context, string, func(*os.File) error) error) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > limit {
		return errPrivateQueueStateLimit
	}
	return save(ctx, path, func(f *os.File) error { _, err := f.Write(data); return err })
}
