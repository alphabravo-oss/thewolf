import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { KnobsCard, type Knob } from "./knobs";
import { SettingsCard } from "./shared";

const LIMITS: Knob[] = [
  {
    key: "scan_concurrency",
    label: "Scan concurrency",
    help: "0 = automatic sizing from the scanner host. Set a number to cap tools per scan; the shared CPU and memory budget still applies.",
    type: "int",
    min: 0,
    max: 32,
  },
  {
    key: "heavy_scanner_concurrency",
    label: "Heavy tool concurrency",
    help: "0 = automatic. Caps demanding tools such as Semgrep, Bearer, and Grype while leaving room for lighter checks.",
    type: "int",
    min: 0,
    max: 32,
  },
  {
    key: "network_scanner_concurrency",
    label: "Network tool concurrency",
    help: "0 = automatic. Caps tools downloading rules, dependencies, or vulnerability databases at the same time.",
    type: "int",
    min: 0,
    max: 32,
  },
];

function CapacityCard() {
  const q = useQuery({
    queryKey: ["scanner-scheduling"],
    queryFn: async () => (await api.get<{
      concurrency: number; heavy_concurrency: number; network_concurrency: number;
      host_cpus: number; host_memory_bytes: number; cpu_budget: number;
      memory_budget_bytes: number; workers: number; source: string; warning?: string;
    }>("/settings/scheduling")).data,
    staleTime: 60_000,
  });
  const plan = q.data;
  return (
    <SettingsCard title="Scanner scheduling" label="Scanner scheduling" description="What the scheduler resolved for the next scan.">
      {q.isPending ? <p className="text-sm text-muted-foreground">Detecting scanner host…</p> :
       q.isError || !plan ? <p role="alert" className="text-sm text-destructive-text">Could not load scheduling capacity.</p> : <>
        <p className="text-sm">Up to {plan.concurrency} tools per scan · {plan.heavy_concurrency} heavy · {plan.network_concurrency} network</p>
        {plan.host_cpus > 0 && <p className="text-xs text-muted-foreground">
          {plan.host_cpus} CPUs · {(plan.host_memory_bytes / 2 ** 30).toFixed(1)} GiB detected.{" "}
          {plan.workers} {plan.workers === 1 ? "worker" : "workers"}, with a shared budget of {plan.cpu_budget.toFixed(1)} CPUs and {(plan.memory_budget_bytes / 2 ** 30).toFixed(1)} GiB per worker.
          Capacity is reserved for Wolf and the operating system. Tools start only when their resource limits fit.
        </p>}
        <p className="text-xs text-muted-foreground">Recalculated for each new scan using its worker’s Docker host. Existing scans keep their limits.</p>
        {plan.warning && <p className="text-xs text-status-warning">{plan.warning}. Conservative fallback: {plan.concurrency} tools.</p>}
      </>}
    </SettingsCard>
  );
}

export function SchedulingSection() {
  return (
    <div className="space-y-4">
      <CapacityCard />
      <KnobsCard title="Concurrency limits" description="Leave at 0 to let Wolf size them from the scanner host." knobs={LIMITS} />
    </div>
  );
}
