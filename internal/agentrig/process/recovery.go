package process

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

var ErrWritersActive = errors.New("previous command ownership still exists; recovery cannot clear the fence")
var ErrRecoveryScope = errors.New("cannot verify command ownership in this host or process namespace")

type recoveryScope struct {
	Host      string
	Boot      string
	Namespace string
}

type commandEvidence struct {
	Version  int
	Platform string
	Scope    recoveryScope
	Group    int
}

func newEvidence() (commandEvidence, error) {
	scope, err := currentScope()
	if err != nil {
		return commandEvidence{}, fmt.Errorf("read command ownership scope: %w", err)
	}
	return commandEvidence{Version: 1, Platform: runtime.GOOS, Scope: scope}, nil
}

func (e commandEvidence) bytes() []byte { data, _ := json.Marshal(e); return data }

func parseEvidence(data []byte) (commandEvidence, error) {
	var e commandEvidence
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&e); err != nil {
		return e, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return e, errors.New("trailing recovery evidence")
	}
	if e.Version != 1 || e.Platform != runtime.GOOS || !validDigest(e.Scope.Host) || !validDigest(e.Scope.Boot) || !validDigest(e.Scope.Namespace) {
		return e, ErrRecoveryScope
	}
	if e.Group < 0 || e.Group == 1 || e.Group > 1<<31-1 {
		return e, errors.New("invalid recorded process group")
	}
	return e, nil
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func validDigest(value string) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == sha256.Size && value == strings.ToLower(value)
}

// RecoverStore attempts proof-based recovery of one local store. It never kills
// processes or resets a fence on elapsed time, missing PIDs, user assertions, or
// legacy records. A live owner, damaged record, or unverifiable scope stays
// fenced. Recovery only restores coordination: callers must still repair/audit
// staging and confirm publication before acknowledging any queued work.
// This milestone supports Linux/macOS; Windows fences remain unrecoverable.
func RecoverStore(ctx context.Context, dir string) (bool, error) {
	return storelock.RecoverFence(ctx, dir, func(data []byte) error {
		e, err := parseEvidence(data)
		if err != nil {
			return err
		}
		scope, err := currentScope()
		if err != nil {
			return err
		}
		if scope.Host != e.Scope.Host {
			return ErrRecoveryScope
		}
		// A different boot of the same host has no surviving old processes.
		if scope.Boot != e.Scope.Boot {
			return nil
		}
		if scope.Namespace != e.Scope.Namespace {
			return ErrRecoveryScope
		}
		return verifyOwnership(e)
	})
}

func normalizeScopeID(value string) (string, error) {
	value = strings.ReplaceAll(strings.TrimSpace(value), "-", "")
	b, err := hex.DecodeString(value)
	if err != nil || len(b) != 16 || bytes.Equal(b, make([]byte, 16)) {
		return "", ErrRecoveryScope
	}
	return hex.EncodeToString(b), nil
}
