// @vitest-environment node
import { afterEach, expect, it } from "vitest";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { ciScript } from "./ci-workflow";

const dirs: string[] = [];
afterEach(() => dirs.splice(0).forEach((dir) => rmSync(dir, { recursive: true })));

it.each([
  ["completed", "pass", "commit", 0],
  ["completed", "warn", "commit", 0],
  ["completed", "incomplete", "commit", 1],
  ["completed", "fail", "commit", 1],
  ["failed", "pass", "commit", 1],
  ["cancelled", "pass", "commit", 1],
  ["completed", "pass", "wrong-commit", 1],
])("CI rejects unsafe results: %s / %s / %s", (status, gate, revision, exit) => {
  const dir = mkdtempSync(join(tmpdir(), "wolf-ci-"));
  dirs.push(dir);
  writeFileSync(join(dir, "curl"), `#!/usr/bin/env bash
set -eu
case "$*" in
  *' -X POST '*)
    [[ "$*" == *'Idempotency-Key: job-1'* ]] || exit 2
    [[ "$*" == *'"branch": "commit"'* ]] || exit 2
    echo '{"data":{"id":"scan-1"}}' ;;
  *'/scan-1/gate'*) echo '{"data":{"evaluation":{"status":"${gate}"}}}' ;;
  *'/scan-1'*) echo '{"data":{"status":"${status}","commit_sha":"${revision}"}}' ;;
  *) exit 2 ;;
esac`, { mode: 0o700 });
  const result = spawnSync("bash", ["-c", ciScript], {
    env: { ...process.env, PATH: `${dir}:${process.env.PATH}`, WOLF_URL: "https://wolf.test",
      WOLF_TOKEN: "test", WOLF_REPO_ID: "repo-1", WOLF_REVISION: "commit", WOLF_REQUEST_KEY: "job-1" },
    timeout: 5000, encoding: "utf8",
  });
  expect(result.status, result.stderr).toBe(exit);
});
