package commitartifact

import (
	"context"
	"io"
	"math"
	"os"
	"strings"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
)

// QueueReclamationResult reports this attempt, including partial progress on
// error. ProtectionReason is pending-work, empty-history or retained-state when archives
// were conservatively retained. Scratch reclamation can still proceed.
type QueueReclamationResult struct {
	WorkspaceCleanupResult
	RemovedArchives     int
	RemovedArchiveBytes int64
	ProtectionReason    string
}

// ReclaimQueueArtifacts requires a live queue.Maintain proof. The stores must be
// exclusively used by that queue and staging directory, with no independent
// artifact consumers. All writers must honor the existing ownership contract.
// It adds queue-parent confirmation cleanup to CleanupWorkspaces. Sealed archive
// reclamation requires completed queue history, an empty durably reflushed queue,
// and no unknown/recovery
// state in any private artifact store. Every candidate archive is verified before
// any deletion. An unfinished batch, even without saved references, protects all
// archives, including output saved before an uncertain phase acknowledgement.
// Shared seeds are retained until commits and captures have been removed.
// This is explicit internal maintenance, not automatic retention or queue repair.
func ReclaimQueueArtifacts(ctx context.Context, req WorkspaceCleanup, proof *queue.Maintenance) (QueueReclamationResult, error) {
	plan := &queueReclamation{proof: proof, unlink: func(root *os.Root, name string) error { return root.Remove(name) }}
	workspace, err := cleanupOwnedWorkspaces(ctx, req, func(root *os.Root, name string) error { return root.RemoveAll(name) }, plan)
	plan.result.WorkspaceCleanupResult = workspace
	return plan.result, err
}

type sealedCandidate struct {
	root      *os.Root
	name, key string
	info      os.FileInfo
	store     artifact.Store
}
type queueReclamation struct {
	proof    *queue.Maintenance
	result   QueueReclamationResult
	archives []sealedCandidate
	unlink   func(*os.Root, string) error
}

func (p *queueReclamation) prepare(ctx context.Context, req WorkspaceCleanup, roots []workspaceRoot) error {
	pending, err := p.proof.HasPending()
	if err != nil {
		return err
	}
	if pending {
		p.result.ProtectionReason = "pending-work"
		return nil
	}
	history, err := p.proof.HasHistory()
	if err != nil {
		return err
	}
	if !history {
		p.result.ProtectionReason = "empty-history"
		return nil
	}
	stores := []artifact.Store{req.Captures, SeedStore(req.Captures), req.Commits}
	// Delete commits before captures, then seeds. Partial cleanup must not leave
	// a retained capture whose shared seed was deleted first.
	for _, i := range []int{2, 0, 1} {
		r := roots[i]
		if r.root == nil {
			continue
		}
		stores[i].Dir = r.path
		archives, protected, err := sealedInventory(ctx, r, stores[i], i == 0)
		if err != nil {
			return err
		}
		if protected {
			p.result.ProtectionReason = "retained-state"
		}
		p.archives = append(p.archives, archives...)
	}
	if p.result.ProtectionReason != "" {
		p.archives = nil
		return nil
	}
	var total int64
	for _, a := range p.archives {
		if err := ctx.Err(); err != nil {
			return err
		}
		if a.info.Size() > math.MaxInt64-total {
			return ErrInvalid
		}
		total += a.info.Size()
		if _, err := a.store.Reference(ctx, a.key); err != nil {
			return err
		}
	}
	return p.proof.Check()
}

func sealedInventory(ctx context.Context, r workspaceRoot, store artifact.Store, capture bool) ([]sealedCandidate, bool, error) {
	directory, err := r.root.Open(".")
	if err != nil {
		return nil, false, err
	}
	defer directory.Close()
	var candidates []sealedCandidate
	protected := false
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		entries, err := directory.ReadDir(128)
		if err != nil && err != io.EOF {
			return nil, false, err
		}
		for _, entry := range entries {
			count++
			if count > 100000 {
				return nil, false, ErrInvalid
			}
			name := entry.Name()
			if strings.HasSuffix(name, ".capture") {
				st, err := entry.Info()
				if err != nil {
					return nil, false, err
				}
				if !st.Mode().IsRegular() {
					return nil, false, ErrInvalid
				}
				info, err := archiveIdentity(r.root.Open(name))
				if err != nil {
					return nil, false, err
				}
				candidates = append(candidates, sealedCandidate{r.root, name, strings.TrimSuffix(name, ".capture"), info, store})
				continue
			}
			if capture && name == "seeds" && entry.IsDir() {
				continue
			}
			if capture && name == ".seeds.agentrig.lock" && entry.Type().IsRegular() {
				continue
			}
			if strings.HasPrefix(name, ".durable-") && len(name) > len(".durable-") && entry.Type().IsRegular() {
				continue
			}
			workspace := false
			for _, prefix := range r.prefixes {
				if strings.HasPrefix(name, prefix) && len(name) > len(prefix) && entry.IsDir() {
					workspace = true
				}
			}
			if !workspace {
				protected = true
			}
		}
		if err == io.EOF {
			return candidates, protected, nil
		}
	}
}

func archiveIdentity(file *os.File, err error) (os.FileInfo, error) {
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 {
		return nil, ErrInvalid
	}
	return info, nil
}

func (p *queueReclamation) remove(ctx context.Context) error {
	for _, a := range p.archives {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := p.proof.Check(); err != nil {
			return err
		}
		st, err := a.root.Lstat(a.name)
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			return ErrInvalid
		}
		st, err = archiveIdentity(a.root.Open(a.name))
		if err != nil {
			return err
		}
		if !os.SameFile(st, a.info) || st.Size() != a.info.Size() {
			return ErrInvalid
		}
		if err := p.unlink(a.root, a.name); err != nil {
			return err
		}
		p.result.RemovedArchives++
		p.result.RemovedArchiveBytes += st.Size()
	}
	return nil
}
