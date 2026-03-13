"use client";

import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { StatusBadge } from "@/components/status-badge";
import { LoadingSpinner } from "@/components/loading-spinner";
import { EmptyState } from "@/components/empty-state";
import api from "@/lib/api";
import type { Loop } from "@/lib/types";

export default function LoopDashboardPage() {
  const { id } = useParams<{ id: string }>();
  const [loop, setLoop] = useState<Loop | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    api
      .get<Loop>(`/loops/${id}`)
      .then((res) => setLoop(res.data))
      .catch(() => {})
      .finally(() => setLoading(false));
  }, [id]);

  const handlePause = async () => {
    try {
      const res = await api.put<Loop>(`/loops/${id}/pause`);
      setLoop(res.data);
    } catch {
      // error handled by api layer
    }
  };

  const handleResume = async () => {
    try {
      const res = await api.put<Loop>(`/loops/${id}/resume`);
      setLoop(res.data);
    } catch {
      // error handled by api layer
    }
  };

  if (loading) return <LoadingSpinner />;
  if (!loop) return <EmptyState title="Loop not found" />;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">Loop Dashboard</h1>
          <div className="flex items-center gap-2 mt-1">
            <StatusBadge status={loop.status} />
            <span className="text-muted-foreground">
              Iteration {loop.current_iteration}/{loop.max_iterations}
            </span>
          </div>
        </div>
        <div className="flex gap-2">
          {loop.status === "running" && (
            <Button variant="outline" onClick={handlePause}>
              Pause
            </Button>
          )}
          {loop.status === "paused" && (
            <Button onClick={handleResume}>Resume</Button>
          )}
        </div>
      </div>

      <div className="grid gap-4 md:grid-cols-4">
        <Card>
          <CardContent className="pt-6 text-center">
            <p className="text-3xl font-bold">
              {loop.total_findings_initial}
            </p>
            <p className="text-sm text-muted-foreground">Initial Findings</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="pt-6 text-center">
            <p className="text-3xl font-bold text-green-600">
              {loop.total_findings_fixed}
            </p>
            <p className="text-sm text-muted-foreground">Fixed</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="pt-6 text-center">
            <p className="text-3xl font-bold text-orange-600">
              {loop.total_findings_new}
            </p>
            <p className="text-sm text-muted-foreground">New</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="pt-6 text-center">
            <p className="text-3xl font-bold">
              {loop.total_findings_remaining}
            </p>
            <p className="text-sm text-muted-foreground">Remaining</p>
          </CardContent>
        </Card>
      </div>

      {loop.guardrail_warnings && loop.guardrail_warnings.length > 0 && (
        <Card className="border-yellow-500">
          <CardHeader>
            <CardTitle className="text-sm text-yellow-600">
              Guardrail Warnings
            </CardTitle>
          </CardHeader>
          <CardContent>
            <ul className="space-y-1">
              {loop.guardrail_warnings.map((w, i) => (
                <li key={i} className="text-sm text-yellow-700">
                  {w}
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
      )}

      {loop.iterations && loop.iterations.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">Iteration History</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="space-y-2">
              {loop.iterations.map((iter) => (
                <div
                  key={iter.iteration}
                  className="flex items-center justify-between py-2 border-b last:border-0"
                >
                  <span className="font-medium">
                    Iteration {iter.iteration}
                  </span>
                  <div className="flex gap-4 text-sm text-muted-foreground">
                    <span>Before: {iter.findings_before}</span>
                    <span>After: {iter.findings_after}</span>
                    <span className="text-green-600">
                      Fixed: {iter.findings_fixed}
                    </span>
                    <span className="text-orange-600">
                      New: {iter.findings_new}
                    </span>
                  </div>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
