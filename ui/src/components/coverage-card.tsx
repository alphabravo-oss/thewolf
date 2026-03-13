"use client";

import { useState } from "react";
import type { CoverageReport } from "@/lib/types";

function coverageColor(percent: number): string {
  if (percent >= 75) return "text-green-500";
  if (percent >= 50) return "text-yellow-500";
  return "text-red-500";
}

function progressColor(percent: number): string {
  if (percent >= 75) return "bg-green-500";
  if (percent >= 50) return "bg-yellow-500";
  return "bg-red-500";
}

interface CoverageCardProps {
  coverage: CoverageReport;
}

export function CoverageCard({ coverage }: CoverageCardProps) {
  const [showAll, setShowAll] = useState(false);
  const uncoveredLimit = 20;
  const displayedUncovered = showAll
    ? coverage.uncovered_files
    : coverage.uncovered_files.slice(0, uncoveredLimit);
  const hasMore = coverage.uncovered_files.length > uncoveredLimit;

  const languages = Object.entries(coverage.by_language);

  return (
    <div className="rounded-lg border border-zinc-200 bg-white p-6 dark:border-zinc-800 dark:bg-zinc-950">
      <h3 className="text-lg font-semibold mb-4">Test Coverage</h3>

      {/* Overall coverage */}
      <div className="flex items-center gap-6 mb-6">
        <div className="flex-shrink-0">
          <div className="relative w-20 h-20">
            <svg className="w-20 h-20 -rotate-90" viewBox="0 0 36 36">
              <path
                className="text-zinc-200 dark:text-zinc-800"
                d="M18 2.0845
                  a 15.9155 15.9155 0 0 1 0 31.831
                  a 15.9155 15.9155 0 0 1 0 -31.831"
                fill="none"
                stroke="currentColor"
                strokeWidth="3"
              />
              <path
                className={coverageColor(coverage.coverage_percent)}
                d="M18 2.0845
                  a 15.9155 15.9155 0 0 1 0 31.831
                  a 15.9155 15.9155 0 0 1 0 -31.831"
                fill="none"
                stroke="currentColor"
                strokeWidth="3"
                strokeDasharray={`${coverage.coverage_percent}, 100`}
              />
            </svg>
            <span
              className={`absolute inset-0 flex items-center justify-center text-sm font-bold ${coverageColor(coverage.coverage_percent)}`}
            >
              {Math.round(coverage.coverage_percent)}%
            </span>
          </div>
        </div>

        <div className="grid grid-cols-3 gap-4 flex-1">
          <div>
            <p className="text-sm text-zinc-500 dark:text-zinc-400">
              Files with tests
            </p>
            <p className="text-xl font-semibold">
              {coverage.files_with_tests}{" "}
              <span className="text-sm font-normal text-zinc-500">
                / {coverage.total_source_files}
              </span>
            </p>
          </div>
          <div>
            <p className="text-sm text-zinc-500 dark:text-zinc-400">
              Test files
            </p>
            <p className="text-xl font-semibold">{coverage.test_files}</p>
          </div>
          <div>
            <p className="text-sm text-zinc-500 dark:text-zinc-400">
              Uncovered
            </p>
            <p className="text-xl font-semibold text-red-500">
              {coverage.files_without_tests}
            </p>
          </div>
        </div>
      </div>

      {/* Progress bar */}
      <div className="mb-6">
        <div className="h-2 w-full rounded-full bg-zinc-200 dark:bg-zinc-800">
          <div
            className={`h-2 rounded-full transition-all ${progressColor(coverage.coverage_percent)}`}
            style={{ width: `${Math.min(coverage.coverage_percent, 100)}%` }}
          />
        </div>
      </div>

      {/* Per-language breakdown */}
      {languages.length > 0 && (
        <div className="mb-6">
          <h4 className="text-sm font-medium text-zinc-500 dark:text-zinc-400 mb-2">
            By Language
          </h4>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-zinc-200 dark:border-zinc-800">
                  <th className="text-left py-2 pr-4 font-medium">Language</th>
                  <th className="text-right py-2 px-4 font-medium">Source Files</th>
                  <th className="text-right py-2 px-4 font-medium">Test Files</th>
                  <th className="text-right py-2 px-4 font-medium">Coverage</th>
                </tr>
              </thead>
              <tbody>
                {languages.map(([key, lc]) => (
                  <tr
                    key={key}
                    className="border-b border-zinc-100 dark:border-zinc-900"
                  >
                    <td className="py-2 pr-4 capitalize">{lc.language}</td>
                    <td className="text-right py-2 px-4">{lc.source_files}</td>
                    <td className="text-right py-2 px-4">{lc.test_files}</td>
                    <td
                      className={`text-right py-2 px-4 font-medium ${coverageColor(lc.coverage_percent)}`}
                    >
                      {Math.round(lc.coverage_percent)}%
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Uncovered files */}
      {coverage.uncovered_files.length > 0 && (
        <details className="group">
          <summary className="cursor-pointer text-sm font-medium text-zinc-500 dark:text-zinc-400 hover:text-zinc-700 dark:hover:text-zinc-300">
            Uncovered Files ({coverage.uncovered_files.length})
          </summary>
          <ul className="mt-2 space-y-1 text-sm text-zinc-600 dark:text-zinc-400">
            {displayedUncovered.map((file) => (
              <li key={file} className="font-mono truncate">
                {file}
              </li>
            ))}
          </ul>
          {hasMore && !showAll && (
            <button
              onClick={() => setShowAll(true)}
              className="mt-2 text-sm text-blue-500 hover:text-blue-600"
            >
              Show all {coverage.uncovered_files.length} files
            </button>
          )}
          {hasMore && showAll && (
            <button
              onClick={() => setShowAll(false)}
              className="mt-2 text-sm text-blue-500 hover:text-blue-600"
            >
              Show less
            </button>
          )}
        </details>
      )}
    </div>
  );
}
