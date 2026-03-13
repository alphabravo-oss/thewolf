"use client";

import { useState } from "react";
import { useParams, useRouter } from "next/navigation";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { StatusBadge } from "@/components/status-badge";
import { useSSE } from "@/lib/sse";
import type { FixProgressEvent, FixItemStatus } from "@/lib/types";

interface FixProgress {
  finding_id: string;
  status: FixItemStatus;
  current: number;
  total: number;
}

export default function FixLivePage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const [progress, setProgress] = useState<FixProgress | null>(null);
  const [history, setHistory] = useState<FixProgress[]>([]);

  const { connected } = useSSE<FixProgressEvent>({
    path: `/fixes/${id}/stream`,
    onEvent: (event) => {
      if (event.type === "fix_progress") {
        const p: FixProgress = {
          finding_id: event.finding_id,
          status: event.status,
          current: event.current_index,
          total: event.total,
        };
        setProgress(p);
        setHistory((prev) => [...prev, p]);
      }
      if (event.type === ("fix_complete" as string)) {
        router.push(`/fixes/${id}`);
      }
    },
  });

  const fixed = history.filter((h) => h.status === "fixed").length;
  const failed = history.filter((h) => h.status === "failed").length;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">Live Fix Progress</h1>
          <StatusBadge status={connected ? "running" : "pending"} />
        </div>
        <Button variant="outline" onClick={() => router.push(`/fixes/${id}`)}>
          View Results
        </Button>
      </div>

      <div className="grid gap-4 md:grid-cols-3">
        <Card>
          <CardContent className="pt-6 text-center">
            <p className="text-3xl font-bold text-green-600">{fixed}</p>
            <p className="text-sm text-muted-foreground">Fixed</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="pt-6 text-center">
            <p className="text-3xl font-bold text-red-600">{failed}</p>
            <p className="text-sm text-muted-foreground">Failed</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="pt-6 text-center">
            <p className="text-3xl font-bold">
              {progress ? `${progress.current}/${progress.total}` : "0/0"}
            </p>
            <p className="text-sm text-muted-foreground">Progress</p>
          </CardContent>
        </Card>
      </div>

      {progress && (
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">Current Finding</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="flex items-center justify-between">
              <span className="font-mono text-sm">{progress.finding_id}</span>
              <StatusBadge status={progress.status} />
            </div>
            <div className="mt-3 h-2 bg-muted rounded-full overflow-hidden">
              <div
                className="h-full bg-primary rounded-full transition-all duration-300"
                style={{
                  width: `${progress.total > 0 ? (progress.current / progress.total) * 100 : 0}%`,
                }}
              />
            </div>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle className="text-sm">Activity Log</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="space-y-2 max-h-80 overflow-y-auto">
            {history.length === 0 && (
              <p className="text-sm text-muted-foreground">
                Waiting for fix to start...
              </p>
            )}
            {history.map((h, i) => (
              <div
                key={i}
                className="flex items-center justify-between text-sm py-1 border-b last:border-0"
              >
                <span className="font-mono truncate max-w-xs">
                  {h.finding_id}
                </span>
                <StatusBadge status={h.status} />
              </div>
            ))}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
