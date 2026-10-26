package runner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

func TestAutomaticScheduling(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		cpu                            int
		memory                         int64
		workers, total, heavy, network int
	}{
		{"preview host", 16, 30 * gib, 1, 6, 3, 4},
		{"small host", 2, 4 * gib, 1, 1, 1, 1},
		{"memory limited", 32, 4 * gib, 1, 1, 1, 1},
		{"two replicas", 16, 30 * gib, 2, 3, 1, 3},
		{"large host cap", 128, 256 * gib, 1, 32, 16, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := sizeScheduling(container.HostResources{ID: "host", NCPU: tc.cpu, MemTotal: tc.memory}, 2, "2g", tc.workers)
			if p.Concurrency != tc.total || p.HeavyConcurrency != tc.heavy || p.NetworkConcurrency != tc.network {
				t.Fatalf("unexpected plan: %+v", p)
			}
			if p.CPUBudget*float64(tc.workers) >= float64(tc.cpu) || p.MemoryBudget*int64(tc.workers) >= tc.memory {
				t.Fatalf("no headroom reserved: %+v", p)
			}
		})
	}
	p := DetectScheduling(t.Context(), nil, 3, 8, 9)
	if p.Concurrency != 3 || p.HeavyConcurrency != 3 || p.NetworkConcurrency != 3 {
		t.Fatalf("manual ceilings were not applied: %+v", p)
	}
}

func TestResourceAdmissionDoesNotHoldPartialSlots(t *testing.T) {
	p := SchedulingPlan{Concurrency: 3, HeavyConcurrency: 1, NetworkConcurrency: 1}
	slots := newResourceSlots(p)
	releaseHeavy, err := acquireResourceSlots(t.Context(), ResourceSpec{Class: "heavy"}, slots, 1, gib)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseHeavy()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		release, err := acquireResourceSlots(ctx, ResourceSpec{Class: "heavy", NetworkRequired: true}, slots, 1, gib)
		if err == nil {
			release()
		}
		done <- err
	}()
	// A heavy+network waiter must not reserve the network slot while blocked.
	lightCtx, lightCancel := context.WithTimeout(t.Context(), time.Second)
	defer lightCancel()
	releaseLight, err := acquireResourceSlots(lightCtx, ResourceSpec{Class: "medium", NetworkRequired: true}, slots, 1, gib)
	if err != nil {
		t.Fatalf("unrelated network work blocked: %v", err)
	}
	releaseLight()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter: %v", err)
	}
	if slots.active != 1 || slots.network != 0 {
		t.Fatalf("cancelled waiter leaked resources: %+v", slots)
	}
}

func TestConcurrentScansShareMemoryAndCPUBudget(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cpu    float64
		memory int64
	}{
		{"CPU", 2, 8 * gib}, {"memory", 8, 2 * gib},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := SchedulingPlan{Concurrency: 8, HeavyConcurrency: 4, NetworkConcurrency: 4, CPUBudget: tc.cpu, MemoryBudget: tc.memory, hostID: t.Name()}
			a, b := newResourceSlots(plan), newResourceSlots(plan)
			release, err := acquireResourceSlots(t.Context(), ResourceSpec{}, a, 2, 2*gib)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			if next, err := acquireResourceSlots(ctx, ResourceSpec{}, b, 1, gib); !errors.Is(err, context.DeadlineExceeded) {
				if next != nil {
					next()
				}
				t.Fatalf("second scan exceeded %s budget: %v", tc.name, err)
			}
			release()
			release() // completion/cancellation release must be idempotent
			next, err := acquireResourceSlots(t.Context(), ResourceSpec{}, b, 1, gib)
			if err != nil {
				t.Fatalf("resources not released: %v", err)
			}
			next()
			if _, err := acquireResourceSlots(t.Context(), ResourceSpec{}, a, 1, 9*gib); err == nil {
				t.Fatal("oversized scanner should fail instead of waiting forever")
			}
		})
	}
}
