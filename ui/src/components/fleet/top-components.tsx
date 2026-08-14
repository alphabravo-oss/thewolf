// ui/src/components/fleet/top-components.tsx
import { Link } from "@tanstack/react-router";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useTopVulnerableRules } from "@/lib/fleet";

export function TopComponents() {
  const q = useTopVulnerableRules(10);

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Top vulnerable components</CardTitle>
      </CardHeader>
      <CardContent>
        {q.isLoading ? (
          <Skeleton className="h-48 w-full" />
        ) : !q.data || q.data.length === 0 ? (
          <div className="text-sm text-muted-foreground">No findings yet.</div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Rule</TableHead>
                <TableHead className="text-right">Repos</TableHead>
                <TableHead className="text-right">Findings</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {q.data.map((row) => (
                <TableRow key={row.key}>
                  <TableCell className="font-mono text-xs">
                    <Link
                      to="/findings"
                      search={{ view: "rule", q: row.key }}
                      className="hover:underline"
                    >
                      {row.key}
                    </Link>
                  </TableCell>
                  <TableCell className="text-right tabular-nums">{row.repos}</TableCell>
                  <TableCell className="text-right tabular-nums">{row.findings}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}
