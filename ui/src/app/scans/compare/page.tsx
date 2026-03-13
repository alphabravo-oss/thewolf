"use client";

import { useEffect, useState } from "react";
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { SeverityBadge } from "@/components/severity-badge";
import { LoadingSpinner } from "@/components/loading-spinner";
import api from "@/lib/api";
import type { Scan, ComparisonResult, Finding, ChangedFinding } from "@/lib/types";

type Tab = "new" | "fixed" | "changed";

export default function CompareScansPage() {
  const [scans, setScans] = useState<Scan[]>([]);
  const [scan1Id, setScan1Id] = useState("");
  const [scan2Id, setScan2Id] = useState("");
  const [result, setResult] = useState<ComparisonResult | null>(null);
  const [loading, setLoading] = useState(true);
  const [comparing, setComparing] = useState(false);
  const [activeTab, setActiveTab] = useState<Tab>("new");

  useEffect(() => {
    api
      .get<Scan[]>("/scans")
      .then((res) => setScans(res.data))
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  const handleCompare = async () => {
    if (!scan1Id || !scan2Id) return;
    setComparing(true);
    try {
      const res = await api.get<ComparisonResult>(
        `/scans/${scan1Id}/compare/${scan2Id}`
      );
      setResult(res.data);
    } catch {
      // error handled by api layer
    } finally {
      setComparing(false);
    }
  };

  if (loading) return <LoadingSpinner />;

  const tabs: { key: Tab; label: string; count: number | undefined }[] = [
    { key: "new", label: "New Findings", count: result?.summary.new_count },
    { key: "fixed", label: "Fixed Findings", count: result?.summary.fixed_count },
    { key: "changed", label: "Changed Findings", count: result?.summary.changed_count },
  ];

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-3xl font-bold">Compare Scans</h1>
        <p className="text-muted-foreground">
          Select two scans to compare findings
        </p>
      </div>

      <Card>
        <CardContent className="pt-6">
          <div className="flex items-end gap-4">
            <div className="flex-1 space-y-2">
              <label className="text-sm font-medium">Base Scan</label>
              <Select value={scan1Id} onValueChange={setScan1Id}>
                <SelectTrigger className="w-full">
                  <SelectValue placeholder="Select base scan" />
                </SelectTrigger>
                <SelectContent>
                  {scans.map((s) => (
                    <SelectItem key={s.id} value={s.id}>
                      {s.branch} - {new Date(s.created_at).toLocaleString()} ({s.finding_count} findings)
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <div className="flex-1 space-y-2">
              <label className="text-sm font-medium">Compare Scan</label>
              <Select value={scan2Id} onValueChange={setScan2Id}>
                <SelectTrigger className="w-full">
                  <SelectValue placeholder="Select compare scan" />
                </SelectTrigger>
                <SelectContent>
                  {scans.map((s) => (
                    <SelectItem key={s.id} value={s.id}>
                      {s.branch} - {new Date(s.created_at).toLocaleString()} ({s.finding_count} findings)
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <Button
              onClick={handleCompare}
              disabled={!scan1Id || !scan2Id || scan1Id === scan2Id || comparing}
            >
              {comparing ? "Comparing..." : "Compare"}
            </Button>
          </div>
        </CardContent>
      </Card>

      {result && (
        <>
          {/* Delta percentage */}
          <div className="text-center">
            <span
              className={`text-4xl font-bold ${
                result.summary.delta_percent > 0
                  ? "text-red-500"
                  : result.summary.delta_percent < 0
                    ? "text-green-500"
                    : "text-gray-500"
              }`}
            >
              {result.summary.delta_percent > 0 ? "+" : ""}
              {result.summary.delta_percent.toFixed(1)}%
            </span>
            <p className="text-sm text-muted-foreground mt-1">
              Finding count change
            </p>
          </div>

          {/* Summary cards */}
          <div className="grid gap-4 md:grid-cols-4">
            <Card>
              <CardContent className="pt-6 text-center">
                <p className="text-sm text-muted-foreground">New</p>
                <p className="text-3xl font-bold text-red-500">
                  {result.summary.new_count}
                </p>
              </CardContent>
            </Card>
            <Card>
              <CardContent className="pt-6 text-center">
                <p className="text-sm text-muted-foreground">Fixed</p>
                <p className="text-3xl font-bold text-green-500">
                  {result.summary.fixed_count}
                </p>
              </CardContent>
            </Card>
            <Card>
              <CardContent className="pt-6 text-center">
                <p className="text-sm text-muted-foreground">Unchanged</p>
                <p className="text-3xl font-bold text-gray-500">
                  {result.summary.unchanged_count}
                </p>
              </CardContent>
            </Card>
            <Card>
              <CardContent className="pt-6 text-center">
                <p className="text-sm text-muted-foreground">Changed</p>
                <p className="text-3xl font-bold text-yellow-500">
                  {result.summary.changed_count}
                </p>
              </CardContent>
            </Card>
          </div>

          {/* Tabs */}
          <div className="border-b">
            <div className="flex gap-4">
              {tabs.map(({ key, label, count }) => (
                <button
                  key={key}
                  onClick={() => setActiveTab(key)}
                  className={`pb-2 text-sm font-medium border-b-2 transition-colors ${
                    activeTab === key
                      ? "border-primary text-primary"
                      : "border-transparent text-muted-foreground hover:text-foreground"
                  }`}
                >
                  {label}
                  {count !== undefined && (
                    <span className="ml-1.5 text-xs text-muted-foreground">
                      ({count})
                    </span>
                  )}
                </button>
              ))}
            </div>
          </div>

          {/* Tab content */}
          {activeTab === "new" && (
            <FindingsTable findings={result.new_findings} emptyMessage="No new findings" />
          )}
          {activeTab === "fixed" && (
            <FindingsTable findings={result.fixed_findings} emptyMessage="No fixed findings" />
          )}
          {activeTab === "changed" && (
            <ChangedFindingsTable findings={result.changed_findings} />
          )}
        </>
      )}
    </div>
  );
}

function FindingsTable({
  findings,
  emptyMessage,
}: {
  findings: Finding[];
  emptyMessage: string;
}) {
  if (findings.length === 0) {
    return (
      <p className="text-center text-muted-foreground py-8">{emptyMessage}</p>
    );
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Severity</TableHead>
          <TableHead>Title</TableHead>
          <TableHead>Tool</TableHead>
          <TableHead>File</TableHead>
          <TableHead>Line</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {findings.map((f) => (
          <TableRow key={f.id}>
            <TableCell>
              <SeverityBadge severity={f.severity} />
            </TableCell>
            <TableCell className="font-medium">{f.title}</TableCell>
            <TableCell className="font-mono text-sm">{f.tool_name}</TableCell>
            <TableCell className="font-mono text-sm max-w-[200px] truncate">
              {f.file_path}
            </TableCell>
            <TableCell>{f.line_start}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function ChangedFindingsTable({
  findings,
}: {
  findings: ChangedFinding[];
}) {
  if (findings.length === 0) {
    return (
      <p className="text-center text-muted-foreground py-8">
        No changed findings
      </p>
    );
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Title</TableHead>
          <TableHead>Before</TableHead>
          <TableHead>After</TableHead>
          <TableHead>File</TableHead>
          <TableHead>Line</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {findings.map((cf) => (
          <TableRow key={cf.before.id}>
            <TableCell className="font-medium">{cf.before.title}</TableCell>
            <TableCell>
              <SeverityBadge severity={cf.before.severity} />
            </TableCell>
            <TableCell>
              <SeverityBadge severity={cf.after.severity} />
            </TableCell>
            <TableCell className="font-mono text-sm max-w-[200px] truncate">
              {cf.after.file_path}
            </TableCell>
            <TableCell>{cf.after.line_start}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
