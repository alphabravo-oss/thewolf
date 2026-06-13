// ui/src/components/fleet/needs-attention.tsx
import { Link } from "@tanstack/react-router";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useNeedsAttention, type NeedsAttentionRow } from "@/lib/fleet";

const reasonLabel: Record<NeedsAttentionRow["reason"], string> = {
  gate_failing: "Gate failing",
  stale: "Stale scan",
  new_high: "New high",
  scan_failed: "Scan failed",
};

const reasonTone: Record<NeedsAttentionRow["reason"], "destructive" | "secondary" | "outline" | "default"> = {
  gate_failing: "destructive",
  scan_failed: "destructive",
  new_high: "default",
  stale: "secondary",
};

export function NeedsAttention() {
  const q = useNeedsAttention();

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Needs attention</CardTitle>
      </CardHeader>
      <CardContent>
        {q.isLoading ? (
          <Skeleton className="h-48 w-full" />
        ) : !q.data || q.data.length === 0 ? (
          <div className="text-sm text-muted-foreground">All clear.</div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Repo</TableHead>
                <TableHead>Reason</TableHead>
                <TableHead>Detail</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {q.data.map((row) => (
                <TableRow key={row.repo_id}>
                  <TableCell>
                    <Link
                      to="/repos/$repoId"
                      params={{ repoId: row.repo_id }}
                      className="hover:underline font-medium"
                    >
                      {row.name}
                    </Link>
                  </TableCell>
                  <TableCell>
                    <Badge variant={reasonTone[row.reason]}>{reasonLabel[row.reason]}</Badge>
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">{row.detail}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}
