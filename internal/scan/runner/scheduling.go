package runner

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
	"sync"

	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

const gib = int64(1 << 30)

type SchedulingPlan struct {
	Concurrency        int     `json:"concurrency"`
	HeavyConcurrency   int     `json:"heavy_concurrency"`
	NetworkConcurrency int     `json:"network_concurrency"`
	HostCPUs           int     `json:"host_cpus"`
	HostMemory         int64   `json:"host_memory_bytes"`
	CPUBudget          float64 `json:"cpu_budget"`
	MemoryBudget       int64   `json:"memory_budget_bytes"`
	Workers            int     `json:"workers"`
	Source             string  `json:"source"`
	Warning            string  `json:"warning,omitempty"`
	hostID             string
}

// DetectScheduling recalculates capacity for each scan, including remote Docker
// engines. Explicit limits remain per-scan ceilings inside the host budget.
func DetectScheduling(ctx context.Context, cfg *container.Config, concurrency, heavy, network int) SchedulingPlan {
	plan := SchedulingPlan{Concurrency: 2, HeavyConcurrency: 1, NetworkConcurrency: 2, Workers: 1, Source: "conservative fallback"}
	if cfg != nil && !cfg.Disabled {
		host, err := container.DetectHostResources(ctx, cfg)
		if err == nil {
			workers, _ := strconv.Atoi(os.Getenv("WOLF_SCAN_WORKERS"))
			plan = sizeScheduling(host, container.CPUCount(cfg), container.MemoryLimitFor(cfg, ""), max(1, workers))
		} else {
			plan.Warning = err.Error()
		}
	}
	if concurrency > 0 {
		plan.Concurrency = concurrency
	}
	if heavy > 0 {
		plan.HeavyConcurrency = heavy
	}
	if network > 0 {
		plan.NetworkConcurrency = network
	}
	plan.HeavyConcurrency = min(plan.HeavyConcurrency, plan.Concurrency)
	plan.NetworkConcurrency = min(plan.NetworkConcurrency, plan.Concurrency)
	return plan
}

func sizeScheduling(host container.HostResources, cpus float64, memory string, workers int) SchedulingPlan {
	// Reserve at least one CPU and 2 GiB, or 25% on larger hosts, for Wolf,
	// the database, Docker, and the OS. Replicas share the remaining budget.
	cpuBudget := max(0.25, float64(host.NCPU)-max(1, float64(host.NCPU)*0.25)) / float64(workers)
	memoryBudget := max(host.MemTotal/2, host.MemTotal-max(2*gib, host.MemTotal/4)) / int64(workers)
	mem, err := container.MemoryBytes(memory)
	if err != nil {
		mem = memoryBudget
	}
	count := max(1, min(32, int(math.Floor(cpuBudget/cpus)), int(memoryBudget/mem)))
	return SchedulingPlan{
		Concurrency: count, HeavyConcurrency: max(1, count/2), NetworkConcurrency: min(4, count),
		HostCPUs: host.NCPU, HostMemory: host.MemTotal, CPUBudget: cpuBudget, MemoryBudget: memoryBudget,
		Workers: workers, Source: "Docker host", hostID: host.ID,
	}
}

// One mutex admits all resource dimensions together. A waiting heavy/network
// tool holds no slots, so unrelated work can still start.
type resourcePool struct {
	mu         sync.Mutex
	changed    chan struct{}
	cpuUsed    float64
	memoryUsed int64
}

type resourceSlots struct {
	pool                              *resourcePool
	plan                              SchedulingPlan
	active, heavy, network, exclusive int
}

var hostPools sync.Map

func newResourceSlots(plan SchedulingPlan) *resourceSlots {
	pool := &resourcePool{changed: make(chan struct{})}
	if plan.hostID != "" {
		// ponytail: one shared pool per Docker daemon in this worker process.
		// Compose replicas split its budget via WOLF_SCAN_WORKERS. Independent
		// deployments need explicit budgets or a future distributed allocator.
		value, _ := hostPools.LoadOrStore(plan.hostID, pool)
		pool = value.(*resourcePool)
	}
	return &resourceSlots{pool: pool, plan: plan}
}

func acquireResourceSlots(ctx context.Context, spec ResourceSpec, slots *resourceSlots, cpus float64, memory int64) (func(), error) {
	heavy := spec.Class == "heavy" || spec.Class == "exclusive" || spec.Exclusive
	network := spec.NetworkRequired || spec.Class == "network"
	exclusive := spec.Exclusive || spec.Class == "exclusive"
	p := slots.pool
	limits := slots.plan
	if limits.CPUBudget > 0 && (cpus > limits.CPUBudget || memory > limits.MemoryBudget) {
		return nil, fmt.Errorf("scanner needs %.2f CPUs and %.1f GiB, exceeds worker budget %.2f CPUs / %.1f GiB", cpus, float64(memory)/float64(gib), limits.CPUBudget, float64(limits.MemoryBudget)/float64(gib))
	}
	for {
		p.mu.Lock()
		if err := ctx.Err(); err != nil {
			p.mu.Unlock()
			return nil, err
		}
		fits := slots.active < limits.Concurrency &&
			(!heavy || slots.heavy < limits.HeavyConcurrency) &&
			(!network || slots.network < limits.NetworkConcurrency) &&
			(!exclusive || slots.exclusive == 0) &&
			(limits.CPUBudget == 0 || (p.cpuUsed+cpus <= limits.CPUBudget && p.memoryUsed+memory <= limits.MemoryBudget))
		if fits {
			slots.active++
			if heavy {
				slots.heavy++
			}
			if network {
				slots.network++
			}
			if exclusive {
				slots.exclusive++
			}
			p.cpuUsed += cpus
			p.memoryUsed += memory
			p.mu.Unlock()
			return sync.OnceFunc(func() {
				p.mu.Lock()
				defer p.mu.Unlock()
				slots.active--
				if heavy {
					slots.heavy--
				}
				if network {
					slots.network--
				}
				if exclusive {
					slots.exclusive--
				}
				p.cpuUsed -= cpus
				p.memoryUsed -= memory
				close(p.changed)
				p.changed = make(chan struct{})
			}), nil
		}
		changed := p.changed
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}
