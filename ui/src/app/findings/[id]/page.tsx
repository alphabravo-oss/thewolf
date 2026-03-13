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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { SeverityBadge } from "@/components/severity-badge";
import { StatusBadge } from "@/components/status-badge";
import { ScoreBar } from "@/components/score-bar";
import { CodeSnippet } from "@/components/code-snippet";
import { LoadingSpinner } from "@/components/loading-spinner";
import { EmptyState } from "@/components/empty-state";
import api from "@/lib/api";
import type { Finding, FindingStatus } from "@/lib/types";

export default function FindingDetailPage() {
  const { id } = useParams<{ id: string }>();
  const [finding, setFinding] = useState<Finding | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    api
      .get<Finding>(`/findings/${id}`)
      .then((res) => setFinding(res.data))
      .catch(() => {})
      .finally(() => setLoading(false));
  }, [id]);

  const handleStatusChange = async (status: FindingStatus) => {
    try {
      const res = await api.put<Finding>(`/findings/${id}`, { status });
      setFinding(res.data);
    } catch {
      // error handled by api layer
    }
  };

  const handleFix = async () => {
    try {
      await api.post("/fixes", { finding_ids: [id] });
    } catch {
      // error handled by api layer
    }
  };

  if (loading) return <LoadingSpinner />;
  if (!finding) return <EmptyState title="Finding not found" />;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div className="space-y-1">
          <div className="flex items-center gap-3">
            <SeverityBadge severity={finding.severity} />
            <StatusBadge status={finding.status} />
          </div>
          <h1 className="text-2xl font-bold">{finding.title}</h1>
          <p className="text-muted-foreground">
            {finding.tool_name} &middot; {finding.category}
            {finding.cwe_id && ` · CWE-${finding.cwe_id}`}
          </p>
        </div>
        <div className="flex gap-2">
          <Select
            value={finding.status}
            onValueChange={(v) => handleStatusChange(v as FindingStatus)}
          >
            <SelectTrigger className="w-[140px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="open">Open</SelectItem>
              <SelectItem value="fixed">Fixed</SelectItem>
              <SelectItem value="wont_fix">Won&apos;t Fix</SelectItem>
              <SelectItem value="false_positive">False Positive</SelectItem>
            </SelectContent>
          </Select>
          <Button onClick={handleFix}>Fix with AI</Button>
        </div>
      </div>

      <p className="text-sm">{finding.description}</p>

      <div className="grid gap-4 md:grid-cols-3">
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">Tool Severity</CardTitle>
          </CardHeader>
          <CardContent>
            <ScoreBar score={finding.tool_severity_score * 10} />
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">Location Weight</CardTitle>
          </CardHeader>
          <CardContent>
            <ScoreBar score={finding.location_weight * 10} />
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">AI Context Score</CardTitle>
          </CardHeader>
          <CardContent>
            <ScoreBar score={finding.ai_context_score * 10} />
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="text-sm">Composite Score</CardTitle>
        </CardHeader>
        <CardContent>
          <ScoreBar score={finding.composite_score} />
        </CardContent>
      </Card>

      {finding.code_snippet && (
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">Code</CardTitle>
          </CardHeader>
          <CardContent>
            <CodeSnippet
              code={finding.code_snippet}
              filePath={finding.file_path}
              lineStart={finding.line_start}
              lineEnd={finding.line_end}
            />
          </CardContent>
        </Card>
      )}

      {finding.ai_fix_suggestion && (
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">AI Fix Suggestion</CardTitle>
          </CardHeader>
          <CardContent>
            <pre className="whitespace-pre-wrap text-sm bg-muted p-4 rounded-md">
              {finding.ai_fix_suggestion}
            </pre>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
