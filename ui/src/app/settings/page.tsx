"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@/components/ui/tabs";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { LoadingSpinner } from "@/components/loading-spinner";
import { Badge } from "@/components/ui/badge";
import api, { getToken } from "@/lib/api";
import type {
  Secret,
  SecretKeyType,
  AppSettings,
  AIProvider,
  AIPromptTemplate,
  FixEngine,
  FixMode,
} from "@/lib/types";

interface PluginInfo {
  name: string;
  description?: string;
  project_url?: string;
  category: string;
  languages: string[];
  available: boolean;
  version?: string;
  installable?: boolean;
  install_via?: string;
  install_methods?: { requires: string; cmd: string }[];
}

const categoryLabels: Record<string, string> = {
  sast: "Static Analysis (SAST)",
  sca: "Software Composition Analysis",
  secrets: "Secrets Detection",
  quality: "Code Quality",
  container: "Container Security",
  docs: "Documentation",
  license: "License Compliance",
  sbom: "Software Bill of Materials",
  infra: "Infrastructure as Code",
  dast: "Dynamic Analysis (DAST)",
};

const pluginDetails: Record<string, string> = {
  semgrep:
    "Lightweight, multi-language static analysis engine. Supports 30+ languages with community and Pro rulesets covering OWASP Top 10, injection flaws, and framework-specific security patterns. Runs locally with no code upload required.",
  trivy:
    "Comprehensive security scanner by Aqua Security. Detects vulnerabilities in OS packages, language dependencies, IaC misconfigurations, secrets, and license issues across container images, file systems, and git repos.",
  gitleaks:
    "Fast, regex-based secrets scanner that detects hardcoded passwords, API keys, tokens, and private keys in git history and live files. Includes 150+ built-in rules for popular services (AWS, GitHub, Slack, etc.).",
  bandit:
    "Security-focused static analyzer for Python. Identifies common security issues such as hardcoded passwords, SQL injection, insecure use of subprocess, XML parsing vulnerabilities, and weak cryptography.",
  ruff:
    "Extremely fast Python linter and formatter written in Rust. Replaces Flake8, isort, pyupgrade, and dozens of other tools. Supports 800+ rules with autofix capabilities at 10-100x the speed of traditional linters.",
  eslint:
    "The standard JavaScript and TypeScript linter. Extensible plugin system with thousands of community rules covering code quality, best practices, accessibility, React/Vue/Angular patterns, and security.",
  gosec:
    "Go security checker that scans source code for common vulnerabilities including SQL injection, command injection, hardcoded credentials, weak crypto, and unsafe memory operations. Maps findings to CWE IDs.",
  "golangci-lint":
    "Meta-linter for Go that runs 100+ linters in parallel. Includes errcheck, govet, staticcheck, gosimple, ineffassign, and many more. Configurable via YAML with fast incremental analysis.",
  clippy:
    "Official Rust linter that catches common mistakes, suggests idiomatic patterns, and identifies potential performance issues. Integrated into the Rust toolchain via rustup with 600+ lint rules.",
  hadolint:
    "Dockerfile best-practice linter powered by ShellCheck. Validates Dockerfile instructions against best practices, identifies deprecated syntax, and checks embedded shell commands for common pitfalls.",
  syft:
    "Software Bill of Materials (SBOM) generator by Anchore. Creates SBOMs in SPDX and CycloneDX formats from container images, file systems, and archives. Supports 30+ package ecosystems.",
  mypy:
    "Static type checker for Python that verifies type annotations and catches type errors before runtime. Supports gradual typing, allowing incremental adoption across large codebases.",
  radon:
    "Python code complexity analyzer. Computes Cyclomatic Complexity, Halstead metrics, and Maintainability Index scores. Helps identify overly complex functions that are hard to test and maintain.",
  vulture:
    "Dead code finder for Python. Detects unused functions, variables, imports, classes, and attributes. Reduces codebase size and maintenance burden by identifying code that can be safely removed.",
  "pip-audit":
    "Dependency vulnerability scanner for Python packages maintained by PyPA. Checks installed packages against the Python Packaging Advisory Database and OSV for known security vulnerabilities.",
  shellcheck:
    "Static analysis tool for shell scripts (bash, sh, dash, ksh). Identifies common bugs, syntax issues, portability problems, and suggests improvements. Used by major CI/CD platforms.",
  staticcheck:
    "Advanced static analyzer for Go code. Goes beyond basic linting to find bugs, suggest simplifications, enforce style rules, and identify deprecated API usage. Part of the go-tools suite.",
  checkov:
    "Infrastructure-as-code scanner by Bridgecrew. Scans Terraform, CloudFormation, Kubernetes, Helm, ARM, and Serverless templates for security misconfigurations with 1,000+ built-in policies.",
  trufflehog:
    "Deep secret scanner by Truffle Security. Scans git history, S3 buckets, file systems, and more for 800+ credential types. Verifies discovered secrets against live APIs to confirm they are active.",
  grype:
    "Container and filesystem vulnerability scanner by Anchore. Matches packages against multiple vulnerability databases (NVD, GitHub, OS-specific). Pairs with Syft for SBOM-based scanning.",
  dockle:
    "Container image linter that checks for CIS Docker Benchmark compliance, best practices, and security issues. Scans built images without needing the Dockerfile source.",
  vale:
    "Prose linter for technical documentation. Enforces style guides (Microsoft, Google, write-good) with customizable rules. Supports Markdown, AsciiDoc, reStructuredText, and HTML.",
  spectral:
    "OpenAPI and JSON/YAML linter by Stoplight. Validates API specifications against best practices and custom rulesets. Catches breaking changes, naming inconsistencies, and missing documentation.",
  codeql:
    "Semantic code analysis engine by GitHub. Treats code as data and runs queries to find vulnerability patterns across codebases. Supports C/C++, C#, Go, Java, JavaScript, Python, Ruby, and Swift.",
  scancode:
    "License and copyright scanner by AboutCode. Detects licenses, copyrights, dependencies, and related origin information in source code. Supports 100+ license types.",
  pmd:
    "Source code analyzer for Java, Apex, PLSQL, and other languages. Finds common programming flaws like unused variables, empty catch blocks, unnecessary object creation, and copy-paste code.",
  "npm-audit":
    "Built-in npm dependency vulnerability checker. Scans your project's dependency tree against the npm advisory database for known security vulnerabilities and suggests fixes.",
  cppcheck:
    "Static analysis tool for C and C++ code. Detects undefined behavior, memory leaks, buffer overflows, null pointer dereferences, and MISRA C/C++ compliance issues. Low false-positive rate.",
  phpstan:
    "Static analysis tool for PHP that finds bugs without running code. Supports progressive strictness levels (0-9), understands PHPDoc annotations, and catches type errors, dead code, and logic mistakes.",
  brakeman:
    "Security scanner specifically built for Ruby on Rails applications. Detects SQL injection, XSS, CSRF, mass assignment, command injection, and 30+ other vulnerability types in Rails code.",
  rubocop:
    "Ruby static code analyzer and formatter based on the community Ruby Style Guide. Extensible with plugins for Rails, RSpec, and performance rules. Supports autocorrection for most offenses.",
  govulncheck:
    "Official Go vulnerability scanner maintained by the Go security team at Google. Uses reachability analysis to only report vulnerabilities in code paths your application actually uses — dramatically reducing noise.",
  swiftlint:
    "Swift style and conventions linter used by most major iOS/macOS projects. Enforces community style guide rules, detects force unwrapping, unused code, and complexity issues. Supports custom rules.",
  sqlfluff:
    "Dialect-aware SQL linter and auto-formatter. Supports ANSI SQL, PostgreSQL, MySQL, BigQuery, Snowflake, Databricks, and Jinja/dbt templating. Auto-fixes formatting and style violations.",
  infer:
    "Deep static analyzer created by Meta (Facebook). Performs inter-procedural analysis to find null pointer dereferences, resource leaks, race conditions, and memory issues in C, C++, Java, and Objective-C code.",
  tflint:
    "Pluggable Terraform linter that catches errors terraform plan cannot. Plugin architecture for AWS, Azure, and GCP provider-specific rules. Detects invalid instance types, deprecated resources, and naming conventions.",
  kubescape:
    "Kubernetes security platform and CNCF project. Scans clusters, YAML manifests, and Helm charts against NSA-CISA, MITRE ATT&CK, and CIS Benchmark frameworks. Provides risk scores and remediation guidance.",
  "kube-linter":
    "Static analysis tool for Kubernetes manifests and Helm charts by StackRox (Red Hat). Checks for security misconfigurations, missing resource limits, privilege escalation, and best practice violations.",
  nuclei:
    "Fast, template-based vulnerability scanner by ProjectDiscovery. 11,000+ community templates covering CVEs, misconfigurations, default credentials, and exposed panels. Supports HTTP, DNS, TCP, and more.",
  "osv-scanner":
    "Multi-ecosystem dependency vulnerability scanner by Google. Uses the OSV database covering Go, npm, PyPI, Maven, Cargo, and more. V2 includes reachability analysis and HTML reporting.",
  "detect-secrets":
    "Enterprise-friendly secrets scanner by Yelp. Uses a baseline approach to prevent new secrets from entering code without flooding developers with historical findings. Lower false-positive rate than alternatives.",
};

export default function SettingsPage() {
  const [secrets, setSecrets] = useState<Secret[]>([]);
  const [plugins, setPlugins] = useState<PluginInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [newKeyType, setNewKeyType] = useState<SecretKeyType>("anthropic_key");
  const [newKeyName, setNewKeyName] = useState("");
  const [newKeyValue, setNewKeyValue] = useState("");
  const [password, setPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [installing, setInstalling] = useState<string | null>(null);
  const [installLog, setInstallLog] = useState<string[]>([]);
  const [installError, setInstallError] = useState<string | null>(null);
  const [selectedPlugin, setSelectedPlugin] = useState<PluginInfo | null>(null);
  const logEndRef = useRef<HTMLDivElement>(null);

  const loadPlugins = useCallback(() => {
    return api
      .get<PluginInfo[]>("/config/plugins")
      .then((res) => {
        const list = res.data ?? [];
        setPlugins(list);
        // Refresh selected plugin if it's open
        setSelectedPlugin((prev) =>
          prev ? list.find((p) => p.name === prev.name) ?? null : null
        );
      })
      .catch(() => {});
  }, []);

  useEffect(() => {
    Promise.all([
      api
        .get<Secret[]>("/config/secrets")
        .catch(() => ({ data: [] as Secret[] })),
      api
        .get<PluginInfo[]>("/config/plugins")
        .catch(() => ({ data: [] as PluginInfo[] })),
    ])
      .then(([secretsRes, pluginsRes]) => {
        setSecrets(secretsRes.data ?? []);
        setPlugins(pluginsRes.data ?? []);
      })
      .finally(() => setLoading(false));
  }, []);

  // Auto-scroll install log within its container
  useEffect(() => {
    const el = logEndRef.current?.parentElement;
    if (el) el.scrollTop = el.scrollHeight;
  }, [installLog]);

  const handleInstall = async (name: string) => {
    setInstalling(name);
    setInstallLog([]);
    setInstallError(null);

    const apiUrl =
      process.env.NEXT_PUBLIC_API_URL || "http://localhost:8778/api";
    const token = getToken();
    const headers: Record<string, string> = {};
    if (token) {
      headers["Authorization"] = `Bearer ${token}`;
    }

    try {
      const response = await fetch(`${apiUrl}/config/plugins/${name}/install`, {
        method: "POST",
        headers,
        credentials: "include",
      });

      if (!response.ok) {
        const err = await response.json().catch(() => null);
        setInstallError(
          err?.error?.message || `Install failed (HTTP ${response.status})`
        );
        setInstalling(null);
        return;
      }

      const reader = response.body?.getReader();
      const decoder = new TextDecoder();

      if (!reader) {
        setInstallError("Streaming not supported");
        setInstalling(null);
        return;
      }

      let buffer = "";

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split("\n");
        buffer = lines.pop() || "";

        for (const line of lines) {
          if (line.startsWith("data: ")) {
            const data = line.slice(6);
            // Check if it's the "done" event's JSON
            try {
              const parsed = JSON.parse(data);
              if (parsed.installed) {
                // Success — refresh plugin list
                await loadPlugins();
                setInstalling(null);
                return;
              }
              if (parsed.error) {
                setInstallError(parsed.error);
                setInstalling(null);
                return;
              }
            } catch {
              // Not JSON — it's a log line
              setInstallLog((prev) => [...prev, data]);
            }
          }
        }
      }

      // Stream ended — refresh regardless
      await loadPlugins();
    } catch (err) {
      setInstallError(
        err instanceof Error ? err.message : "Connection failed"
      );
    } finally {
      setInstalling(null);
    }
  };

  const handleAddKey = async () => {
    try {
      const res = await api.post<Secret>("/config/secrets", {
        key_type: newKeyType,
        key_name: newKeyName || newKeyType,
        value: newKeyValue,
      });
      setSecrets([...secrets, res.data]);
      setNewKeyName("");
      setNewKeyValue("");
    } catch {
      // error handled by api layer
    }
  };

  const handleDeleteKey = async (id: string) => {
    try {
      await api.delete(`/config/secrets/${id}`);
      setSecrets(secrets.filter((s) => s.id !== id));
    } catch {
      // error handled by api layer
    }
  };

  const handleChangePassword = async () => {
    try {
      await api.put("/auth/password", {
        current_password: password,
        new_password: newPassword,
      });
      setPassword("");
      setNewPassword("");
    } catch {
      // error handled by api layer
    }
  };

  if (loading) return <LoadingSpinner />;

  const installedCount = plugins.filter((p) => p.available).length;
  const missingPlugins = plugins.filter((p) => !p.available);
  const installableCount = missingPlugins.filter(
    (p) => p.installable
  ).length;

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-3xl font-bold">Settings</h1>
        <p className="text-muted-foreground">
          Manage API keys, credentials, and preferences
        </p>
      </div>

      <Tabs defaultValue="api-keys">
        <TabsList>
          <TabsTrigger value="scan">Scan</TabsTrigger>
          <TabsTrigger value="api-keys">API Keys</TabsTrigger>
          <TabsTrigger value="git">Git Credentials</TabsTrigger>
          <TabsTrigger value="plugins">Plugins</TabsTrigger>
          <TabsTrigger value="ai">AI Assessment</TabsTrigger>
          <TabsTrigger value="fix-engine">Fix Engine</TabsTrigger>
          <TabsTrigger value="profile">Profile</TabsTrigger>
        </TabsList>

        <TabsContent value="scan" className="space-y-4">
          <ScanSettingsSection />
        </TabsContent>

        <TabsContent value="api-keys" className="space-y-4">
          <Card>
            <CardHeader>
              <CardTitle className="text-sm">Add API Key</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <div className="grid gap-3 md:grid-cols-3">
                <div className="space-y-1">
                  <Label htmlFor="api-key-type">Type</Label>
                  <Select
                    value={newKeyType}
                    onValueChange={(v) => setNewKeyType(v as SecretKeyType)}
                  >
                    <SelectTrigger id="api-key-type">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="anthropic_key">Anthropic</SelectItem>
                      <SelectItem value="openai_key">OpenAI</SelectItem>
                      <SelectItem value="custom">Custom</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <div className="space-y-1">
                  <Label htmlFor="api-key-name">Name</Label>
                  <Input
                    id="api-key-name"
                    value={newKeyName}
                    onChange={(e) => setNewKeyName(e.target.value)}
                    placeholder="my-key"
                  />
                </div>
                <div className="space-y-1">
                  <Label htmlFor="api-key-value">Value</Label>
                  <Input
                    id="api-key-value"
                    type="password"
                    value={newKeyValue}
                    onChange={(e) => setNewKeyValue(e.target.value)}
                    placeholder="sk-..."
                  />
                </div>
              </div>
              <Button onClick={handleAddKey} size="sm">
                Add Key
              </Button>
            </CardContent>
          </Card>

          {secrets
            .filter(
              (s) =>
                s.key_type === "anthropic_key" ||
                s.key_type === "openai_key" ||
                s.key_type === "custom"
            )
            .map((secret) => (
              <Card key={secret.id}>
                <CardContent className="flex items-center justify-between pt-6">
                  <div>
                    <p className="font-medium">{secret.key_name}</p>
                    <p className="text-sm text-muted-foreground">
                      {secret.key_type} &middot; {secret.masked_value}
                    </p>
                  </div>
                  <Button
                    variant="destructive"
                    size="sm"
                    onClick={() => handleDeleteKey(secret.id)}
                  >
                    Remove
                  </Button>
                </CardContent>
              </Card>
            ))}
        </TabsContent>

        <TabsContent value="git" className="space-y-4">
          <Card>
            <CardHeader>
              <CardTitle className="text-sm">Add Git Token</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <div className="grid gap-3 md:grid-cols-3">
                <div className="space-y-1">
                  <Label htmlFor="git-token-type">Type</Label>
                  <Select
                    value={newKeyType}
                    onValueChange={(v) => setNewKeyType(v as SecretKeyType)}
                  >
                    <SelectTrigger id="git-token-type">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="github_token">GitHub</SelectItem>
                      <SelectItem value="gitlab_token">GitLab</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <div className="space-y-1">
                  <Label htmlFor="git-token-name">Name</Label>
                  <Input
                    id="git-token-name"
                    value={newKeyName}
                    onChange={(e) => setNewKeyName(e.target.value)}
                    placeholder="my-github-token"
                  />
                </div>
                <div className="space-y-1">
                  <Label htmlFor="git-token-value">Token</Label>
                  <Input
                    id="git-token-value"
                    type="password"
                    value={newKeyValue}
                    onChange={(e) => setNewKeyValue(e.target.value)}
                    placeholder="ghp_..."
                  />
                </div>
              </div>
              <Button onClick={handleAddKey} size="sm">
                Add Token
              </Button>
            </CardContent>
          </Card>

          {secrets
            .filter(
              (s) =>
                s.key_type === "github_token" || s.key_type === "gitlab_token"
            )
            .map((secret) => (
              <Card key={secret.id}>
                <CardContent className="flex items-center justify-between pt-6">
                  <div>
                    <p className="font-medium">{secret.key_name}</p>
                    <p className="text-sm text-muted-foreground">
                      {secret.key_type} &middot; {secret.masked_value}
                    </p>
                  </div>
                  <Button
                    variant="destructive"
                    size="sm"
                    onClick={() => handleDeleteKey(secret.id)}
                  >
                    Remove
                  </Button>
                </CardContent>
              </Card>
            ))}
        </TabsContent>

        <TabsContent value="plugins" className="space-y-4">
          <Card>
            <CardHeader>
              <CardTitle className="text-sm">
                Analysis Tools ({installedCount}/{plugins.length} installed)
              </CardTitle>
            </CardHeader>
            <CardContent>
              <p className="text-sm text-muted-foreground mb-4">
                These are the static analysis and security tools Wolf uses
                during scans. Install missing tools directly from here, or run{" "}
                <code className="bg-muted px-1 rounded text-xs">
                  wolf setup
                </code>{" "}
                from the CLI.
              </p>
              {installableCount > 0 && (
                <p className="text-sm">
                  <span className="font-medium text-primary">
                    {installableCount}
                  </span>{" "}
                  tool{installableCount !== 1 ? "s" : ""} can be installed from
                  here.
                </p>
              )}
            </CardContent>
          </Card>

          {Object.entries(
            plugins.reduce<Record<string, PluginInfo[]>>((acc, p) => {
              const cat = p.category || "general";
              if (!acc[cat]) acc[cat] = [];
              acc[cat].push(p);
              return acc;
            }, {})
          )
            .sort(([a], [b]) => a.localeCompare(b))
            .map(([category, categoryPlugins]) => (
              <Card key={category}>
                <CardHeader>
                  <CardTitle className="text-sm capitalize">
                    {category}{" "}
                    <span className="text-muted-foreground font-normal">
                      (
                      {
                        categoryPlugins.filter((p) => p.available).length
                      }
                      /{categoryPlugins.length} available)
                    </span>
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
                    {categoryPlugins.map((p) => (
                      <div
                        key={p.name}
                        role="button"
                        tabIndex={0}
                        className="flex items-center justify-between rounded-md border p-3 text-left hover:bg-muted/50 transition-colors cursor-pointer w-full"
                        onClick={() => setSelectedPlugin(p)}
                        onKeyDown={(e) => {
                          if (e.key === "Enter" || e.key === " ") {
                            e.preventDefault();
                            setSelectedPlugin(p);
                          }
                        }}
                      >
                        <div className="min-w-0 flex-1">
                          <p className="font-medium text-sm">{p.name}</p>
                          {p.available && p.version && (
                            <p className="text-xs text-muted-foreground truncate">
                              {p.version}
                            </p>
                          )}
                          {!p.available && p.install_via && (
                            <p className="text-xs text-muted-foreground">
                              via {p.install_via}
                            </p>
                          )}
                          {(p.languages?.length ?? 0) > 0 && (
                            <p className="text-xs text-muted-foreground">
                              {p.languages?.join(", ")}
                            </p>
                          )}
                        </div>
                        <div className="flex items-center gap-2 ml-2">
                          {p.available ? (
                            <Badge variant="default">Installed</Badge>
                          ) : p.installable ? (
                            <Button
                              size="sm"
                              variant="outline"
                              disabled={installing !== null}
                              onClick={(e) => {
                                e.stopPropagation();
                                setSelectedPlugin(p);
                                handleInstall(p.name);
                              }}
                            >
                              {installing === p.name
                                ? "Installing..."
                                : "Install"}
                            </Button>
                          ) : (
                            <Badge variant="secondary">Missing</Badge>
                          )}
                        </div>
                      </div>
                    ))}
                  </div>
                </CardContent>
              </Card>
            ))}

          {plugins.length === 0 && (
            <Card>
              <CardContent className="py-8 text-center text-muted-foreground">
                No plugins loaded. Make sure the API server is running.
              </CardContent>
            </Card>
          )}
        </TabsContent>

        <TabsContent value="ai" className="space-y-4">
          <AIAssessmentSection />
        </TabsContent>

        <TabsContent value="fix-engine" className="space-y-4">
          <FixEngineSection />
        </TabsContent>

        <TabsContent value="profile" className="space-y-4">
          <Card>
            <CardHeader>
              <CardTitle className="text-sm">Change Password</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <div className="space-y-1">
                <Label htmlFor="current-password">Current Password</Label>
                <Input
                  id="current-password"
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
              </div>
              <div className="space-y-1">
                <Label htmlFor="new-password">New Password</Label>
                <Input
                  id="new-password"
                  type="password"
                  value={newPassword}
                  onChange={(e) => setNewPassword(e.target.value)}
                />
              </div>
              <Button onClick={handleChangePassword} size="sm">
                Update Password
              </Button>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>

      {/* Plugin detail modal */}
      <Dialog
        open={selectedPlugin !== null}
        onOpenChange={(open) => {
          if (!open && !installing) setSelectedPlugin(null);
        }}
      >
        <DialogContent className="max-w-2xl max-h-[85vh] overflow-y-auto">
          {selectedPlugin && (
            <>
              <DialogHeader>
                <DialogTitle className="flex items-center gap-3 text-xl">
                  {selectedPlugin.name}
                  {selectedPlugin.available ? (
                    <Badge variant="default">Installed</Badge>
                  ) : selectedPlugin.installable ? (
                    <Badge variant="secondary">Not Installed</Badge>
                  ) : (
                    <Badge variant="outline">Unavailable</Badge>
                  )}
                </DialogTitle>
                <DialogDescription className="text-sm leading-relaxed pt-1">
                  {pluginDetails[selectedPlugin.name] ||
                    selectedPlugin.description ||
                    `Details for ${selectedPlugin.name}`}
                </DialogDescription>
              </DialogHeader>

              <div className="space-y-5 pt-2">
                {/* Info grid */}
                <div className="grid grid-cols-2 sm:grid-cols-3 gap-4 text-sm">
                  <div className="space-y-1">
                    <p className="text-muted-foreground text-xs font-medium uppercase tracking-wider">
                      Category
                    </p>
                    <p className="capitalize font-medium">
                      {categoryLabels[selectedPlugin.category] ||
                        selectedPlugin.category}
                    </p>
                  </div>
                  {(selectedPlugin.languages?.length ?? 0) > 0 && (
                    <div className="space-y-1">
                      <p className="text-muted-foreground text-xs font-medium uppercase tracking-wider">
                        Languages
                      </p>
                      <div className="flex flex-wrap gap-1">
                        {selectedPlugin.languages?.map((lang) => (
                          <Badge
                            key={lang}
                            variant="outline"
                            className="text-xs font-normal"
                          >
                            {lang}
                          </Badge>
                        ))}
                      </div>
                    </div>
                  )}
                  {selectedPlugin.available && selectedPlugin.version && (
                    <div className="space-y-1">
                      <p className="text-muted-foreground text-xs font-medium uppercase tracking-wider">
                        Version
                      </p>
                      <p className="font-mono text-xs bg-muted rounded px-2 py-1 inline-block">
                        {selectedPlugin.version}
                      </p>
                    </div>
                  )}
                </div>

                {/* Project link */}
                {selectedPlugin.project_url && (
                  <div className="space-y-1">
                    <p className="text-muted-foreground text-xs font-medium uppercase tracking-wider">
                      Project
                    </p>
                    <a
                      href={selectedPlugin.project_url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex items-center gap-1.5 text-sm text-primary hover:underline"
                    >
                      {selectedPlugin.project_url.replace(
                        /^https?:\/\/(www\.)?/,
                        ""
                      )}
                      <svg
                        className="h-3.5 w-3.5"
                        fill="none"
                        viewBox="0 0 24 24"
                        strokeWidth={2}
                        stroke="currentColor"
                      >
                        <path
                          strokeLinecap="round"
                          strokeLinejoin="round"
                          d="M13.5 6H5.25A2.25 2.25 0 003 8.25v10.5A2.25 2.25 0 005.25 21h10.5A2.25 2.25 0 0018 18.75V10.5m-4.5-6H18m0 0v4.5m0-4.5L10.5 13.5"
                        />
                      </svg>
                    </a>
                  </div>
                )}

                {/* Install methods */}
                {(selectedPlugin.install_methods?.length ?? 0) > 0 && (
                  <div className="space-y-2">
                    <p className="text-muted-foreground text-xs font-medium uppercase tracking-wider">
                      Install Methods
                    </p>
                    <div className="space-y-1.5">
                      {selectedPlugin.install_methods?.map((m, i) => (
                        <div
                          key={i}
                          className="bg-muted rounded-md px-3 py-2 font-mono text-xs break-all flex items-start gap-2"
                        >
                          {m.requires && (
                            <Badge
                              variant="outline"
                              className="text-[10px] shrink-0 mt-0.5"
                            >
                              {m.requires}
                            </Badge>
                          )}
                          <span className="text-muted-foreground select-all">
                            {m.cmd}
                          </span>
                        </div>
                      ))}
                    </div>
                  </div>
                )}

                {/* Install button */}
                {!selectedPlugin.available && selectedPlugin.installable && (
                  <Button
                    className="w-full"
                    size="lg"
                    disabled={installing !== null}
                    onClick={() => handleInstall(selectedPlugin.name)}
                  >
                    {installing === selectedPlugin.name
                      ? "Installing..."
                      : `Install ${selectedPlugin.name}`}
                  </Button>
                )}

                {/* Install log inside modal */}
                {installing === selectedPlugin.name &&
                  installLog.length > 0 && (
                    <div className="bg-muted rounded-md p-3 max-h-64 overflow-y-auto font-mono text-xs">
                      {installLog.map((line, i) => (
                        <div key={i} className="whitespace-pre-wrap">
                          {line}
                        </div>
                      ))}
                      <div ref={logEndRef} />
                    </div>
                  )}
                {installing === selectedPlugin.name && installError && (
                  <div className="text-destructive text-sm">{installError}</div>
                )}
              </div>
            </>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}

// ---------------------------------------------------------------------------
// AI Assessment Section
// ---------------------------------------------------------------------------

type PromptType = "tool_assess" | "executive_summary";
type PromptSection = "system_context" | "scoring_criteria" | "output_instructions";

const PROMPT_SECTIONS: { key: PromptSection; label: string }[] = [
  { key: "system_context", label: "System Context" },
  { key: "scoring_criteria", label: "Scoring Criteria" },
  { key: "output_instructions", label: "Output Instructions" },
];

function ScanSettingsSection() {
  const queryClient = useQueryClient();

  const { data: settings = {} } = useQuery({
    queryKey: ["settings"],
    queryFn: () => api.get<AppSettings>("/settings").then((r) => r.data ?? {}),
    retry: false,
  });

  const settingsMutation = useMutation({
    mutationFn: (patch: AppSettings) => api.put("/settings", patch),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["settings"] }),
  });

  const concurrency = parseInt(settings.scan_concurrency || "8", 10) || 8;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm">Scan Execution</CardTitle>
        <CardDescription>
          Control how scans run across your repositories.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-6">
        <div className="space-y-2">
          <Label htmlFor="scan-concurrency">Tool Concurrency</Label>
          <p className="text-xs text-muted-foreground">
            Maximum number of analysis tools to run in parallel during a scan.
            Lower values reduce system load; higher values complete scans faster.
          </p>
          <div className="flex items-center gap-3">
            <Input
              id="scan-concurrency"
              type="number"
              min={1}
              max={32}
              value={concurrency}
              onChange={(e) => {
                const val = parseInt(e.target.value, 10);
                if (val >= 1 && val <= 32) {
                  settingsMutation.mutate({ scan_concurrency: String(val) });
                }
              }}
              className="w-24"
            />
            <span className="text-sm text-muted-foreground">
              tools at a time (default: 8)
            </span>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

interface DefaultPrompts {
  tool_assess: Record<PromptSection, string>;
  executive_summary: Record<PromptSection, string>;
}

function AIAssessmentSection() {
  const queryClient = useQueryClient();

  // ----- Data fetching -----

  const { data: settings = {} } = useQuery({
    queryKey: ["settings"],
    queryFn: () =>
      api.get<AppSettings>("/settings").then((r) => r.data ?? {}),
    retry: false,
  });

  const { data: providers = [] } = useQuery({
    queryKey: ["ai-providers"],
    queryFn: () =>
      api.get<AIProvider[]>("/ai-providers").then((r) => r.data ?? []),
    retry: false,
  });

  const { data: globalPrompts = [] } = useQuery({
    queryKey: ["ai-prompts", "global"],
    queryFn: () =>
      api
        .get<AIPromptTemplate[]>("/ai-prompts?scope=global")
        .then((r) => r.data ?? []),
    retry: false,
  });

  const { data: defaults } = useQuery({
    queryKey: ["ai-prompts-defaults"],
    queryFn: () =>
      api.get<DefaultPrompts>("/ai-prompts/defaults").then((r) => r.data),
    retry: false,
  });

  // ----- Mutations -----

  const settingsMutation = useMutation({
    mutationFn: (patch: AppSettings) => api.put("/settings", patch),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["settings"] }),
  });

  const promptMutation = useMutation({
    mutationFn: (template: {
      scope: string;
      scope_id: string;
      prompt_type: string;
      section: string;
      content: string;
    }) => api.put("/ai-prompts", template),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: ["ai-prompts"] }),
  });

  // ----- Derived state -----

  const aiEnabled = settings.ai_enabled === "true";

  // Build a lookup for existing overrides
  const promptLookup = (type: PromptType, section: PromptSection) =>
    globalPrompts.find(
      (p) => p.prompt_type === type && p.section === section
    );

  const getDefault = (type: PromptType, section: PromptSection) =>
    defaults?.[type]?.[section] ?? "";

  const getContent = (type: PromptType, section: PromptSection) => {
    const override = promptLookup(type, section);
    return override?.content ?? "";
  };

  // Provider/model helpers
  const getProvider = (type: PromptType) => {
    const key =
      type === "tool_assess"
        ? "ai_tool_assess_provider"
        : "ai_executive_summary_provider";
    return settings[key] ?? "";
  };

  const getModel = (type: PromptType) => {
    const key =
      type === "tool_assess"
        ? "ai_tool_assess_model"
        : "ai_executive_summary_model";
    return settings[key] ?? "";
  };

  const modelsForProvider = (providerName: string) =>
    providers.find((p) => p.name === providerName)?.models ?? [];

  // ----- Handlers -----

  const handleToggleAI = (checked: boolean) => {
    settingsMutation.mutate({ ai_enabled: checked ? "true" : "false" });
  };

  const handleProviderChange = (type: PromptType, provider: string) => {
    const providerKey =
      type === "tool_assess"
        ? "ai_tool_assess_provider"
        : "ai_executive_summary_provider";
    const modelKey =
      type === "tool_assess"
        ? "ai_tool_assess_model"
        : "ai_executive_summary_model";
    // Reset model when provider changes
    const models = modelsForProvider(provider);
    settingsMutation.mutate({
      [providerKey]: provider,
      [modelKey]: models[0] ?? "",
    });
  };

  const handleModelChange = (type: PromptType, model: string) => {
    const key =
      type === "tool_assess"
        ? "ai_tool_assess_model"
        : "ai_executive_summary_model";
    settingsMutation.mutate({ [key]: model });
  };

  const handlePromptSave = (
    type: PromptType,
    section: PromptSection,
    content: string
  ) => {
    promptMutation.mutate({
      scope: "global",
      scope_id: "",
      prompt_type: type,
      section,
      content,
    });
  };

  const handleResetToDefault = (type: PromptType, section: PromptSection) => {
    const existing = promptLookup(type, section);
    if (existing) {
      api.delete(`/ai-prompts/${existing.id}`).then(() => {
        queryClient.invalidateQueries({ queryKey: ["ai-prompts"] });
      });
    }
  };

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle className="text-sm">AI Assessment</CardTitle>
          <CardDescription>
            Configure AI-powered assessment of scan findings. When enabled, each
            scan runs a two-phase AI pipeline: per-tool scoring followed by an
            executive summary.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <div className="flex items-center gap-3">
            <Switch
              id="ai-enabled"
              checked={aiEnabled}
              onCheckedChange={handleToggleAI}
            />
            <Label htmlFor="ai-enabled" className="text-sm font-medium">
              Enable AI Assessment
            </Label>
          </div>
        </CardContent>
      </Card>

      <div
        className={
          aiEnabled
            ? ""
            : "pointer-events-none opacity-40 select-none"
        }
        aria-disabled={!aiEnabled}
      >
        <div className="grid gap-6 lg:grid-cols-2">
          <PromptPhaseColumn
            title="Tool Assessment"
            description="Runs once per tool. Scores each finding for real-world impact and generates a per-tool summary."
            type="tool_assess"
            provider={getProvider("tool_assess")}
            model={getModel("tool_assess")}
            providers={providers}
            modelsForProvider={modelsForProvider}
            getContent={getContent}
            getDefault={getDefault}
            onProviderChange={(p) => handleProviderChange("tool_assess", p)}
            onModelChange={(m) => handleModelChange("tool_assess", m)}
            onPromptSave={(section, content) =>
              handlePromptSave("tool_assess", section, content)
            }
            onReset={(section) =>
              handleResetToDefault("tool_assess", section)
            }
          />
          <PromptPhaseColumn
            title="Executive Summary"
            description="Runs once per scan. Produces a markdown executive summary with prioritized recommendations."
            type="executive_summary"
            provider={getProvider("executive_summary")}
            model={getModel("executive_summary")}
            providers={providers}
            modelsForProvider={modelsForProvider}
            getContent={getContent}
            getDefault={getDefault}
            onProviderChange={(p) =>
              handleProviderChange("executive_summary", p)
            }
            onModelChange={(m) =>
              handleModelChange("executive_summary", m)
            }
            onPromptSave={(section, content) =>
              handlePromptSave("executive_summary", section, content)
            }
            onReset={(section) =>
              handleResetToDefault("executive_summary", section)
            }
          />
        </div>
      </div>
    </>
  );
}

// ---------------------------------------------------------------------------
// Prompt Phase Column (Tool Assessment / Executive Summary)
// ---------------------------------------------------------------------------

interface PromptPhaseColumnProps {
  title: string;
  description: string;
  type: PromptType;
  provider: string;
  model: string;
  providers: AIProvider[];
  modelsForProvider: (provider: string) => string[];
  getContent: (type: PromptType, section: PromptSection) => string;
  getDefault: (type: PromptType, section: PromptSection) => string;
  onProviderChange: (provider: string) => void;
  onModelChange: (model: string) => void;
  onPromptSave: (section: PromptSection, content: string) => void;
  onReset: (section: PromptSection) => void;
}

function PromptPhaseColumn({
  title,
  description,
  type,
  provider,
  model,
  providers,
  modelsForProvider,
  getContent,
  getDefault,
  onProviderChange,
  onModelChange,
  onPromptSave,
  onReset,
}: PromptPhaseColumnProps) {
  const models = modelsForProvider(provider);

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm">{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {/* Provider / Model selectors */}
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1">
            <Label className="text-sm">Provider</Label>
            <Select
              value={provider || undefined}
              onValueChange={onProviderChange}
            >
              <SelectTrigger className="w-full">
                <SelectValue placeholder="Select provider" />
              </SelectTrigger>
              <SelectContent>
                {providers.map((p) => (
                  <SelectItem
                    key={p.name}
                    value={p.name}
                  >
                    {p.name}
                  </SelectItem>
                ))}
                {providers.length === 0 && (
                  <SelectItem value="_none" disabled>
                    No providers available
                  </SelectItem>
                )}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1">
            <Label className="text-sm">Model</Label>
            <Select
              value={model || undefined}
              onValueChange={onModelChange}
              disabled={!provider}
            >
              <SelectTrigger className="w-full">
                <SelectValue placeholder="Select model" />
              </SelectTrigger>
              <SelectContent>
                {models.map((m) => (
                  <SelectItem key={m} value={m}>
                    {m}
                  </SelectItem>
                ))}
                {models.length === 0 && (
                  <SelectItem value="_none" disabled>
                    {provider
                      ? "No models available"
                      : "Select a provider first"}
                  </SelectItem>
                )}
              </SelectContent>
            </Select>
          </div>
        </div>

        {/* Prompt sections */}
        {PROMPT_SECTIONS.map((s) => (
          <PromptSectionEditor
            key={s.key}
            label={s.label}
            content={getContent(type, s.key)}
            defaultContent={getDefault(type, s.key)}
            onSave={(content) => onPromptSave(s.key, content)}
            onReset={() => onReset(s.key)}
          />
        ))}
      </CardContent>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Prompt Section Editor (collapsible textarea with reset)
// ---------------------------------------------------------------------------

interface PromptSectionEditorProps {
  label: string;
  content: string;
  defaultContent: string;
  onSave: (content: string) => void;
  onReset: () => void;
}

function PromptSectionEditor({
  label,
  content,
  defaultContent,
  onSave,
  onReset,
}: PromptSectionEditorProps) {
  const [expanded, setExpanded] = useState(false);
  const effectiveContent = content || defaultContent;
  const [draft, setDraft] = useState(effectiveContent);
  const [saved, setSaved] = useState(false);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Sync draft when external data changes
  useEffect(() => {
    setDraft(content || defaultContent);
  }, [content, defaultContent]);

  const handleBlur = () => {
    const hasOverrideNow = content.length > 0;
    // Save if: text changed from override, or text differs from default (creating new override)
    if (hasOverrideNow ? draft !== content : draft !== defaultContent) {
      if (draft.trim() === "" || draft === defaultContent) {
        // User cleared it or reverted to default text — remove override
        if (hasOverrideNow) onReset();
      } else {
        onSave(draft);
      }
      setSaved(true);
      if (timerRef.current) clearTimeout(timerRef.current);
      timerRef.current = setTimeout(() => setSaved(false), 2000);
    }
  };

  const handleReset = () => {
    setDraft(defaultContent);
    onReset();
  };

  const hasOverride = content.length > 0;

  return (
    <div className="rounded-md border">
      <button
        type="button"
        className="flex w-full items-center justify-between px-3 py-2 text-sm font-medium hover:bg-muted/50 transition-colors"
        onClick={() => setExpanded((v) => !v)}
      >
        <span className="flex items-center gap-2">
          <svg
            className={`h-3 w-3 text-muted-foreground transition-transform ${
              expanded ? "rotate-90" : ""
            }`}
            fill="none"
            viewBox="0 0 24 24"
            strokeWidth={2}
            stroke="currentColor"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              d="M8.25 4.5l7.5 7.5-7.5 7.5"
            />
          </svg>
          {label}
          {hasOverride && (
            <Badge variant="secondary" className="text-[10px] px-1.5 py-0">
              Custom
            </Badge>
          )}
        </span>
        {saved && (
          <span className="text-xs text-green-600 dark:text-green-400">
            Saved
          </span>
        )}
      </button>
      {expanded && (
        <div className="border-t px-3 py-3 space-y-2">
          <Textarea
            className="min-h-[120px] text-xs font-mono"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onBlur={handleBlur}
            placeholder={defaultContent || "Default prompt will be used"}
          />
          <div className="flex items-center justify-between">
            <p className="text-xs text-muted-foreground">
              {hasOverride
                ? "Custom override active"
                : "Using default prompt"}
            </p>
            {hasOverride && (
              <Button
                variant="ghost"
                size="sm"
                className="text-xs h-7"
                onClick={handleReset}
              >
                Reset to Default
              </Button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

function FixEngineSection() {
  const queryClient = useQueryClient();
  const [saving, setSaving] = useState(false);

  const { data: engines = [] } = useQuery<FixEngine[]>({
    queryKey: ["fix-engines"],
    queryFn: () => api.get<FixEngine[]>("/fix-engines").then(r => r.data ?? []),
  });

  const { data: settings } = useQuery<AppSettings>({
    queryKey: ["settings"],
    queryFn: () => api.get<AppSettings>("/settings").then(r => r.data ?? {}),
  });

  const currentEngine = settings?.["fix.engine"] ?? "auto";
  const currentMode = (settings?.["fix.default_mode"] ?? "interactive") as FixMode;
  const currentMaxBudget = settings?.["fix.max_budget_usd"] ?? "";
  const currentMaxTurns = settings?.["fix.max_turns"] ?? "";

  const saveSetting = async (key: string, value: string) => {
    setSaving(true);
    try {
      await api.put("/settings", { [key]: value });
      queryClient.invalidateQueries({ queryKey: ["settings"] });
    } catch (err) {
      console.error("Failed to save setting:", err);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm">Fix Engine Configuration</CardTitle>
        <CardDescription>
          Configure the default AI engine and mode for fix operations.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="space-y-2">
          <Label>Preferred Engine</Label>
          <Select value={currentEngine} onValueChange={(v) => saveSetting("fix.engine", v)}>
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {engines.map((eng) => (
                <SelectItem key={eng.name} value={eng.name}>
                  {eng.label} {eng.available ? "✓" : "(not installed)"}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <p className="text-xs text-muted-foreground">
            The AI CLI tool used to generate code fixes. &quot;Auto&quot; tries available engines in order.
          </p>
        </div>

        <div className="space-y-2">
          <Label>Default Mode</Label>
          <div className="flex items-center gap-4">
            <div className="flex items-center gap-2">
              <Switch
                checked={currentMode === "wolfpack"}
                onCheckedChange={(checked) => saveSetting("fix.default_mode", checked ? "wolfpack" : "interactive")}
              />
              <span className="text-sm">
                {currentMode === "wolfpack" ? "🐺 Wolf Pack (auto-fix)" : "Interactive (review diffs)"}
              </span>
            </div>
          </div>
          <p className="text-xs text-muted-foreground">
            Interactive mode generates diffs for review. Wolf Pack mode auto-fixes and commits without human review.
          </p>
        </div>

        {(currentEngine === "claude-code" || currentEngine === "auto") && (
          <>
            <div className="space-y-2">
              <Label>Max Budget per Finding (USD)</Label>
              <Input
                type="number"
                min="0"
                step="0.10"
                placeholder="No limit"
                defaultValue={currentMaxBudget}
                key={`budget-${currentMaxBudget}`}
                onBlur={(e) => saveSetting("fix.max_budget_usd", e.target.value)}
                className="w-32"
              />
              <p className="text-xs text-muted-foreground">
                Maximum API cost allowed per finding fix. Leave empty for no limit.
              </p>
            </div>

            <div className="space-y-2">
              <Label>Max Turns per Finding</Label>
              <Input
                type="number"
                min="1"
                max="100"
                step="1"
                placeholder="20"
                defaultValue={currentMaxTurns}
                key={`turns-${currentMaxTurns}`}
                onBlur={(e) => saveSetting("fix.max_turns", e.target.value)}
                className="w-32"
              />
              <p className="text-xs text-muted-foreground">
                Maximum agentic turns per finding. Default is 20.
              </p>
            </div>
          </>
        )}

        <div className="space-y-2">
          <Label>Available Engines</Label>
          <div className="grid gap-2">
            {engines.map((eng) => (
              <div key={eng.name} className="flex items-center justify-between text-sm border rounded-md px-3 py-2">
                <div>
                  <span className="font-medium">{eng.label}</span>
                  {eng.binary && <span className="text-muted-foreground ml-2">({eng.binary})</span>}
                </div>
                <span className={eng.available ? "text-green-600" : "text-muted-foreground"}>
                  {eng.available ? "Installed" : "Not found"}
                </span>
              </div>
            ))}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
