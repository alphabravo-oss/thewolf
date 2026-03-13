"use client";

import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
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
import type { Fix } from "@/lib/types";

export default function FixDetailPage() {
  const { id } = useParams<{ id: string }>();
  const [fix, setFix] = useState<Fix | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    api
      .get<Fix>(`/fixes/${id}`)
      .then((res) => setFix(res.data))
      .catch(() => {})
      .finally(() => setLoading(false));
  }, [id]);

  if (loading) return <LoadingSpinner />;
  if (!fix) return <EmptyState title="Fix not found" />;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">Fix Details</h1>
          <div className="flex items-center gap-2 mt-1">
            <StatusBadge status={fix.status} />
            <span className="text-muted-foreground font-mono text-sm">
              {fix.branch_name}
            </span>
          </div>
        </div>
        {fix.status === "running" && (
          <Button asChild>
            <Link href={`/fixes/${id}/live`}>View Live</Link>
          </Button>
        )}
      </div>

      <div className="grid gap-4 md:grid-cols-3">
        <Card>
          <CardContent className="pt-6 text-center">
            <p className="text-3xl font-bold text-green-600">
              {fix.findings_fixed}
            </p>
            <p className="text-sm text-muted-foreground">Fixed</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="pt-6 text-center">
            <p className="text-3xl font-bold text-red-600">
              {fix.findings_failed}
            </p>
            <p className="text-sm text-muted-foreground">Failed</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="pt-6 text-center">
            <p className="text-3xl font-bold">{fix.findings_attempted}</p>
            <p className="text-sm text-muted-foreground">Attempted</p>
          </CardContent>
        </Card>
      </div>

      {fix.pr_urls && fix.pr_urls.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">Pull Requests</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="space-y-2">
              {fix.pr_urls.map((url, i) => (
                <a
                  key={i}
                  href={url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="block text-sm text-blue-600 hover:underline"
                >
                  {url}
                </a>
              ))}
            </div>
          </CardContent>
        </Card>
      )}

      {fix.items && fix.items.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">Fix Items</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="space-y-3">
              {fix.items.map((item) => (
                <div
                  key={item.id}
                  className="flex items-center justify-between py-2 border-b last:border-0"
                >
                  <div>
                    <p className="text-sm font-medium">
                      {item.finding?.title ?? item.finding_id}
                    </p>
                    {item.files_changed && item.files_changed.length > 0 && (
                      <p className="text-xs text-muted-foreground">
                        {item.files_changed.length} file(s) changed
                      </p>
                    )}
                  </div>
                  <div className="flex items-center gap-2">
                    {item.validation_result && (
                      <StatusBadge status={item.validation_result} />
                    )}
                    <StatusBadge status={item.status} />
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
