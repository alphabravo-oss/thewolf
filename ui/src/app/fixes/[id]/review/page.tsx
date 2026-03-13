"use client";

import { useState, useEffect, useRef, useCallback } from "react";
import { useParams, useRouter } from "next/navigation";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Switch } from "@/components/ui/switch";
import { Label } from "@/components/ui/label";
import { LoadingSpinner } from "@/components/loading-spinner";
import { EmptyState } from "@/components/empty-state";
import { SeverityBadge } from "@/components/severity-badge";
import api, { getToken } from "@/lib/api";
import type {
  Fix,
  FixItem,
  Finding,
  FixItemStatus,
  FixMode,
} from "@/lib/types";

const API_BASE = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8778/api";

interface EnrichedFixItem extends FixItem {
  finding?: Finding;
}

interface FixDetail extends Fix {
  items: EnrichedFixItem[];
}

const statusIcon: Record<FixItemStatus, string> = {
  pending: "⬚",
  in_progress: "⏳",
  proposed: "🟡",
  approved: "✅",
  rejected: "❌",
  fixed: "✅",
  failed: "💥",
  skipped: "⏭",
};

const statusLabel: Record<FixItemStatus, string> = {
  pending: "Pending",
  in_progress: "Fixing...",
  proposed: "Proposed",
  approved: "Approved",
  rejected: "Rejected",
  fixed: "Fixed",
  failed: "Failed",
  skipped: "Skipped",
};

export default function FixReviewPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const queryClient = useQueryClient();
  const [selectedItemId, setSelectedItemId] = useState<string | null>(null);
  const [actionLoading, setActionLoading] = useState<string | null>(null);
  const eventSourceRef = useRef<EventSource | null>(null);

  // Fetch fix detail with items
  const { data: fixDetail, isLoading } = useQuery<FixDetail>({
    queryKey: ["fix-detail", id],
    queryFn: () => api.get<FixDetail>(`/fixes/${id}`).then((r) => r.data),
    refetchInterval: 3000,
  });

  // Connect to SSE for real-time updates
  useEffect(() => {
    if (!id) return;
    const token = getToken();
    const url = `${API_BASE}/fixes/${id}/stream${token ? `?token=${encodeURIComponent(token)}` : ""}`;
    const es = new EventSource(url);
    eventSourceRef.current = es;

    es.onmessage = () => {
      // Refetch fix detail on any SSE event
      queryClient.invalidateQueries({ queryKey: ["fix-detail", id] });
    };

    es.onerror = () => {
      es.close();
    };

    return () => {
      es.close();
    };
  }, [id, queryClient]);

  // Auto-select first proposed item
  useEffect(() => {
    if (!fixDetail?.items || selectedItemId) return;
    const proposed = fixDetail.items.find((i) => i.status === "proposed");
    if (proposed) setSelectedItemId(proposed.id);
  }, [fixDetail?.items, selectedItemId]);

  const handleApprove = useCallback(async (itemId: string) => {
    setActionLoading(itemId);
    try {
      await api.post(`/fixes/${id}/items/${itemId}/approve`, {});
      queryClient.invalidateQueries({ queryKey: ["fix-detail", id] });
      // Auto-advance to next proposed
      const items = fixDetail?.items ?? [];
      const idx = items.findIndex((i) => i.id === itemId);
      const next = items.slice(idx + 1).find((i) => i.status === "proposed");
      if (next) setSelectedItemId(next.id);
    } catch (err) {
      console.error("Approve failed:", err);
    } finally {
      setActionLoading(null);
    }
  }, [id, fixDetail?.items, queryClient]);

  const handleReject = useCallback(async (itemId: string) => {
    setActionLoading(itemId);
    try {
      await api.post(`/fixes/${id}/items/${itemId}/reject`, {});
      queryClient.invalidateQueries({ queryKey: ["fix-detail", id] });
      const items = fixDetail?.items ?? [];
      const idx = items.findIndex((i) => i.id === itemId);
      const next = items.slice(idx + 1).find((i) => i.status === "proposed");
      if (next) setSelectedItemId(next.id);
    } catch (err) {
      console.error("Reject failed:", err);
    } finally {
      setActionLoading(null);
    }
  }, [id, fixDetail?.items, queryClient]);

  const handleApproveAll = useCallback(async () => {
    setActionLoading("all");
    try {
      await api.post(`/fixes/${id}/approve-all`, {});
      queryClient.invalidateQueries({ queryKey: ["fix-detail", id] });
    } catch (err) {
      console.error("Approve all failed:", err);
    } finally {
      setActionLoading(null);
    }
  }, [id, queryClient]);

  const handleRejectAll = useCallback(async () => {
    setActionLoading("reject-all");
    try {
      await api.post(`/fixes/${id}/reject-all`, {});
      queryClient.invalidateQueries({ queryKey: ["fix-detail", id] });
    } catch (err) {
      console.error("Reject all failed:", err);
    } finally {
      setActionLoading(null);
    }
  }, [id, queryClient]);

  const handleCancel = useCallback(async () => {
    try {
      await api.delete(`/fixes/${id}`);
      router.push("/");
    } catch (err) {
      console.error("Cancel failed:", err);
    }
  }, [id, router]);

  if (isLoading) return <LoadingSpinner />;
  if (!fixDetail) return <EmptyState title="Fix not found" />;

  const fix = fixDetail;
  const items = fixDetail.items ?? [];
  const selectedItem = items.find((i) => i.id === selectedItemId);

  // Progress counts
  const counts = {
    proposed: items.filter((i) => i.status === "proposed").length,
    fixed: items.filter((i) => i.status === "fixed").length,
    rejected: items.filter((i) => i.status === "rejected").length,
    failed: items.filter((i) => i.status === "failed").length,
    pending: items.filter((i) => i.status === "pending").length,
    in_progress: items.filter((i) => i.status === "in_progress").length,
  };
  const totalProcessed = counts.proposed + counts.fixed + counts.rejected + counts.failed;
  const progressPct = items.length > 0 ? Math.round((totalProcessed / items.length) * 100) : 0;

  return (
    <div className="flex flex-col h-[calc(100vh-4rem)]">
      {/* Header */}
      <div className="border-b px-4 py-3 flex items-center justify-between bg-background">
        <div>
          <h1 className="text-lg font-semibold flex items-center gap-2">
            🐺 Fix Review
            <Badge variant="outline">{fix.id.slice(0, 8)}</Badge>
            <Badge variant={fix.mode === "wolfpack" ? "destructive" : "secondary"}>
              {fix.mode === "wolfpack" ? "🐺 Wolf Pack" : "Interactive"}
            </Badge>
          </h1>
          <p className="text-sm text-muted-foreground">
            Engine: {fix.engine || "auto"} · {items.length} findings · {counts.proposed} proposed · {counts.fixed} fixed · {counts.rejected} rejected
          </p>
        </div>
        <div className="flex items-center gap-2">
          {counts.proposed > 0 && (
            <>
              <Button size="sm" onClick={handleApproveAll} disabled={actionLoading === "all"}>
                {actionLoading === "all" ? "Approving..." : `Approve All (${counts.proposed})`}
              </Button>
              <Button size="sm" variant="outline" onClick={handleRejectAll} disabled={actionLoading === "reject-all"}>
                Reject All
              </Button>
            </>
          )}
          <Button size="sm" variant="destructive" onClick={handleCancel}>
            Cancel
          </Button>
        </div>
      </div>

      {/* Main content */}
      <div className="flex flex-1 overflow-hidden">
        {/* Sidebar - Finding list */}
        <ScrollArea className="w-80 border-r bg-muted/30">
          <div className="p-2 space-y-1">
            {items.map((item) => (
              <button
                key={item.id}
                onClick={() => setSelectedItemId(item.id)}
                className={`w-full text-left px-3 py-2 rounded-md text-sm transition-colors ${
                  selectedItemId === item.id
                    ? "bg-primary text-primary-foreground"
                    : "hover:bg-muted"
                }`}
              >
                <div className="flex items-center gap-2">
                  <span>{statusIcon[item.status]}</span>
                  <span className="truncate flex-1 font-medium">
                    {item.finding?.title ?? item.finding_id.slice(0, 8)}
                  </span>
                </div>
                <div className="flex items-center gap-2 mt-0.5 text-xs opacity-75">
                  {item.finding && <SeverityBadge severity={item.finding.severity} />}
                  <span>{statusLabel[item.status]}</span>
                </div>
              </button>
            ))}
          </div>
        </ScrollArea>

        {/* Main panel - Diff viewer */}
        <div className="flex-1 overflow-auto p-4">
          {selectedItem ? (
            <div className="space-y-4">
              {/* Finding info */}
              <Card>
                <CardHeader className="pb-2">
                  <CardTitle className="text-base flex items-center gap-2">
                    {selectedItem.finding && <SeverityBadge severity={selectedItem.finding.severity} />}
                    {selectedItem.finding?.title ?? "Finding"}
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <div className="text-sm text-muted-foreground space-y-1">
                    {selectedItem.finding && (
                      <>
                        <p><strong>File:</strong> {selectedItem.finding.file_path}:{selectedItem.finding.line_start}</p>
                        <p><strong>Tool:</strong> {selectedItem.finding.tool_name}</p>
                        {selectedItem.finding.description && (
                          <p><strong>Description:</strong> {selectedItem.finding.description}</p>
                        )}
                      </>
                    )}
                    <p><strong>Status:</strong> {statusIcon[selectedItem.status]} {statusLabel[selectedItem.status]}</p>
                  </div>
                </CardContent>
              </Card>

              {/* Diff */}
              {selectedItem.diff && (
                <Card>
                  <CardHeader className="pb-2">
                    <CardTitle className="text-base">Proposed Diff</CardTitle>
                  </CardHeader>
                  <CardContent>
                    <pre className="text-xs overflow-auto bg-muted rounded-md p-3 max-h-[500px]">
                      {selectedItem.diff.split("\n").map((line, i) => {
                        let cls = "";
                        if (line.startsWith("+") && !line.startsWith("+++")) cls = "text-green-600 dark:text-green-400";
                        else if (line.startsWith("-") && !line.startsWith("---")) cls = "text-red-600 dark:text-red-400";
                        else if (line.startsWith("@@")) cls = "text-blue-600 dark:text-blue-400";
                        return (
                          <div key={i} className={cls}>
                            {line}
                          </div>
                        );
                      })}
                    </pre>
                  </CardContent>
                </Card>
              )}

              {/* Error message */}
              {selectedItem.error_message && (
                <Card className="border-red-200 dark:border-red-800">
                  <CardContent className="pt-4">
                    <p className="text-sm text-red-600 dark:text-red-400">{selectedItem.error_message}</p>
                  </CardContent>
                </Card>
              )}

              {/* Actions */}
              {selectedItem.status === "proposed" && (
                <div className="flex gap-2">
                  <Button
                    onClick={() => handleApprove(selectedItem.id)}
                    disabled={!!actionLoading}
                  >
                    {actionLoading === selectedItem.id ? "Applying..." : "✓ Approve"}
                  </Button>
                  <Button
                    variant="outline"
                    onClick={() => handleReject(selectedItem.id)}
                    disabled={!!actionLoading}
                  >
                    ✗ Reject
                  </Button>
                  <Button
                    variant="ghost"
                    onClick={() => {
                      const next = items.find(
                        (i) => i.status === "proposed" && i.id !== selectedItem.id
                      );
                      if (next) setSelectedItemId(next.id);
                    }}
                  >
                    → Skip
                  </Button>
                </div>
              )}
            </div>
          ) : (
            <EmptyState title="Select a finding from the sidebar" />
          )}
        </div>
      </div>

      {/* Progress bar */}
      <div className="border-t px-4 py-2 bg-muted/30 flex items-center gap-4">
        <div className="flex-1 bg-muted rounded-full h-2 overflow-hidden">
          <div
            className="bg-primary h-full transition-all duration-300"
            style={{ width: `${progressPct}%` }}
          />
        </div>
        <span className="text-xs text-muted-foreground whitespace-nowrap">
          {totalProcessed}/{items.length} processed · {counts.proposed} proposed · {counts.fixed} fixed · {counts.rejected} rejected · {counts.failed} failed
          {counts.in_progress > 0 && ` · ${counts.in_progress} in progress`}
        </span>
      </div>
    </div>
  );
}
