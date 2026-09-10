package queue

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

func TestMaintainReflushesBeforeAuthorizingAndExpiresProof(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("completed"))
	w := worker(t, q)
	finish(t, w, next(t, w))
	w.Close()
	if _, err := q.CompactReceipts(t.Context(), fixtureTime.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	before := queueBytes(t, q)
	saves := 0
	q.save = func(dir string, data []byte) error { saves++; return saveFile(dir, data) }
	var retained *Maintenance
	err := q.Maintain(t.Context(), fixtureBinding, func(m *Maintenance) error {
		retained = m
		if history, err := m.HasHistory(); err != nil || !history {
			t.Fatal("lost compacted history", history, err)
		}
		if saves != 1 {
			t.Fatal("callback preceded durable reflush")
		}
		pending, err := m.HasPending()
		if err != nil || pending {
			t.Fatal(pending, err)
		}
		if _, err := q.Worker(t.Context()); !errors.Is(err, storelock.ErrBusy) {
			t.Fatal("worker not excluded", err)
		}
		_, release, err := storelock.Acquire(t.Context(), q.dir, 0)
		if err == nil {
			release()
			t.Fatal("producer transaction not excluded")
		}
		if !errors.Is(err, storelock.ErrBusy) {
			t.Fatal(err)
		}
		return nil
	})
	if err != nil || !bytes.Equal(before, queueBytes(t, q)) {
		t.Fatal("maintenance changed logical bytes", err)
	}
	if !errors.Is(retained.Check(), ErrOwner) {
		t.Fatal("proof outlived callback")
	}
	if !errors.Is((&Maintenance{}).Check(), ErrOwner) {
		t.Fatal("forged empty proof")
	}
}

func TestMaintainRefusesUncertainReflushAndRetainsWork(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("pending"))
	before := queueBytes(t, q)
	for _, mode := range []string{"failure", "uncertain", "success"} {
		called := false
		q.save = func(dir string, data []byte) error {
			if mode == "failure" {
				return os.ErrPermission
			}
			if err := saveFile(dir, data); err != nil {
				return err
			}
			if mode == "uncertain" {
				return ErrUncertain
			}
			return nil
		}
		err := q.Maintain(t.Context(), fixtureBinding, func(m *Maintenance) error {
			called = true
			pending, err := m.HasPending()
			if err != nil || !pending {
				t.Fatal(pending, err)
			}
			return nil
		})
		if mode == "success" {
			if err != nil || !called {
				t.Fatal(called, err)
			}
		} else if err == nil || called {
			t.Fatal("unconfirmed state authorized maintenance", called, err)
		}
		if !bytes.Equal(before, queueBytes(t, q)) {
			t.Fatal("changed pending work")
		}
	}
}

func TestMaintainRefusesInvalidOwnershipStateAndBinding(t *testing.T) {
	for _, mode := range []string{"busy", "fence", "binding", "corrupt", "missing", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			q := fixture(t)
			binding := fixtureBinding
			ctx := t.Context()
			switch mode {
			case "busy":
				w := worker(t, q)
				defer w.Close()
			case "fence":
				held, release, err := storelock.Acquire(ctx, q.dir, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, err = storelock.BeginFence(held)
				release()
				if err != nil {
					t.Fatal(err)
				}
			case "binding":
				binding.ConfigID = "other"
			case "corrupt":
				if err := os.WriteFile(filepath.Join(q.dir, "queue.json"), []byte("damaged"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.RemoveAll(q.dir); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			if err := q.Maintain(ctx, binding, func(*Maintenance) error { t.Fatal("invalid state authorized callback"); return nil }); err == nil {
				t.Fatal("accepted invalid maintenance")
			}
			if mode == "missing" {
				if _, err := os.Stat(q.dir); !os.IsNotExist(err) {
					t.Fatal("created missing queue", err)
				}
			}
		})
	}
}
