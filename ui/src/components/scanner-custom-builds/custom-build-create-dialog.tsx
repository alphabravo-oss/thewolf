import { useEffect, useId, useMemo, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { AlertTriangleIcon, HammerIcon, Loader2Icon } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  CUSTOM_BUILD_PLATFORMS,
  scannerCustomBuildApi,
  validateCustomBuildInput,
  type CustomBuildCreateInput,
  type CustomBuildOperationReceipt,
  type CustomBuildPlatform,
  type CustomBuildVariantName,
} from "@/lib/scanner-custom-build";

type VariantSelection = CustomBuildVariantName | "all";

export interface CustomBuildCreateDefaults {
  variant?: VariantSelection;
  push?: boolean;
  platforms?: CustomBuildPlatform[];
}

export function CustomBuildCreateDialog({
  open,
  onOpenChange,
  defaults,
  onAccepted,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  defaults?: CustomBuildCreateDefaults;
  onAccepted: (receipt: CustomBuildOperationReceipt) => void;
}) {
  const variantId = useId();
  const modeId = useId();
  const namespaceId = useId();
  const reasonId = useId();
  const defaultVariant = defaults?.variant ?? "all";
  const defaultPush = defaults?.push ?? false;
  const defaultPlatforms = defaults?.platforms?.length
    ? defaults.platforms
    : (["linux/amd64"] as CustomBuildPlatform[]);
  const defaultPlatformsKey = defaultPlatforms.join(",");
  const [variant, setVariant] = useState<VariantSelection>(
    defaultVariant,
  );
  const [push, setPush] = useState(defaultPush);
  const [platforms, setPlatforms] = useState<CustomBuildPlatform[]>(
    defaultPlatforms,
  );
  const [namespace, setNamespace] = useState("");
  const [reason, setReason] = useState("");

  useEffect(() => {
    if (!open) return;
    setVariant(defaultVariant);
    setPush(defaultPush);
    setPlatforms(defaultPlatforms);
    setNamespace("");
    setReason("");
  // defaultPlatformsKey intentionally represents the value, not the caller's
  // array identity, so a parent render cannot erase in-progress operator input.
  }, [defaultPlatformsKey, defaultPush, defaultVariant, open]);

  const input = useMemo<CustomBuildCreateInput>(
    () => ({
      variants: [variant],
      push,
      platforms,
      namespace: push && namespace.trim() ? namespace.trim() : undefined,
      reason: reason.trim(),
    }),
    [namespace, platforms, push, reason, variant],
  );
  const errors = validateCustomBuildInput(input);

  const create = useMutation({
    mutationFn: () => scannerCustomBuildApi.create(input),
    onSuccess: (receipt) => {
      toast.success(`Custom build ${receipt.id} queued`);
      onAccepted(receipt);
      onOpenChange(false);
    },
    onError: (error) =>
      toast.error(
        error instanceof Error ? error.message : "Custom build could not be queued",
      ),
  });

  function chooseVariant(next: VariantSelection) {
    setVariant(next);
    if (next === "codeql") setPush(false);
  }

  function choosePush(next: boolean) {
    setPush(next);
    if (!next && platforms.length > 1) setPlatforms([platforms[0]]);
  }

  function togglePlatform(platform: CustomBuildPlatform) {
    setPlatforms((current) => {
      if (!push) return [platform];
      if (current.includes(platform)) {
        return current.length === 1
          ? current
          : current.filter((item) => item !== platform);
      }
      return [...current, platform];
    });
  }

  const codeqlSelected = variant === "codeql" || variant === "all";
  const blockedPush = push && codeqlSelected;

  return (
    <Dialog
      open={open}
      onOpenChange={create.isPending ? undefined : onOpenChange}
    >
      <DialogContent className="max-h-[90vh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Queue durable custom build</DialogTitle>
          <DialogDescription>
            The build continues on a worker after this dialog or browser closes.
            Its status, per-variant result, and bounded logs remain available in
            Custom builds.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-5">
          <div className="space-y-1.5">
            <Label htmlFor={variantId}>Scanner variant</Label>
            <select
              id={variantId}
              value={variant}
              onChange={(event) =>
                chooseVariant(event.target.value as VariantSelection)
              }
              className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              <option value="all">All variants</option>
              <option value="default">Default</option>
              <option value="jvm">JVM</option>
              <option value="rust">Rust</option>
              <option value="codeql">CodeQL</option>
            </select>
            <p className="text-xs text-muted-foreground">
              All builds Default, JVM, Rust, and CodeQL in that order.
            </p>
          </div>

          <fieldset className="space-y-2">
            <legend id={modeId} className="text-sm font-medium">
              Destination
            </legend>
            <div
              className="grid gap-2 sm:grid-cols-2"
              aria-labelledby={modeId}
            >
              <ModeOption
                checked={!push}
                label="Load locally"
                description="Build one platform and load it into this Docker host."
                onChange={() => choosePush(false)}
              />
              <ModeOption
                checked={push}
                disabled={variant === "codeql"}
                label="Push to registry"
                description="Use the configured DockerHub credential; no secret value is shown."
                onChange={() => choosePush(true)}
              />
            </div>
          </fieldset>

          {codeqlSelected ? (
            <div
              className="rounded-md border border-amber-500/30 bg-amber-500/10 p-3 text-sm"
              role="note"
            >
              <div className="flex items-start gap-2">
                <AlertTriangleIcon
                  className="mt-0.5 size-4 shrink-0 text-amber-300"
                  aria-hidden="true"
                />
                <p>
                  <strong>CodeQL is local-only.</strong> It cannot be pushed or
                  redistributed. A push-all request is rejected before submit;
                  use a local all-variant build or push the other variants
                  individually.
                </p>
              </div>
            </div>
          ) : null}

          <fieldset className="space-y-2">
            <legend className="text-sm font-medium">Build platforms</legend>
            <div className="flex flex-wrap gap-3">
              {CUSTOM_BUILD_PLATFORMS.map((platform) => (
                <label
                  key={platform}
                  className="inline-flex items-center gap-2 rounded-md border border-border/70 px-3 py-2 text-sm"
                >
                  <input
                    type={push ? "checkbox" : "radio"}
                    name={push ? undefined : "custom-build-platform"}
                    checked={platforms.includes(platform)}
                    onChange={() => togglePlatform(platform)}
                    className="size-4 accent-primary"
                  />
                  <span className="font-mono text-xs">{platform}</span>
                </label>
              ))}
            </div>
            <p className="text-xs text-muted-foreground">
              Local builds support one platform. Registry builds may publish
              both supported Linux platforms as a multi-architecture manifest.
            </p>
          </fieldset>

          {push ? (
            <div className="space-y-1.5">
              <Label htmlFor={namespaceId}>Registry namespace (optional)</Label>
              <Input
                id={namespaceId}
                name="namespace"
                autoComplete="off"
                maxLength={255}
                value={namespace}
                onChange={(event) => setNamespace(event.target.value)}
                placeholder="alphabravodevops"
              />
              <p className="text-xs text-muted-foreground">
                Leave blank to use the server default. Credentials are resolved
                server-side and are never returned to this page.
              </p>
            </div>
          ) : null}

          <div className="space-y-1.5">
            <Label htmlFor={reasonId}>Reason</Label>
            <textarea
              id={reasonId}
              name="reason"
              value={reason}
              onChange={(event) => setReason(event.target.value)}
              rows={3}
              maxLength={2_048}
              placeholder="Explain why this custom build is required"
              className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            />
            <p className="text-xs text-muted-foreground">
              The reason is retained with the durable operation for audit
              review.
            </p>
          </div>

          {errors.length ? (
            <div
              className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive"
              role="alert"
            >
              <ul className="list-disc space-y-1 pl-5">
                {errors.map((error) => (
                  <li key={error}>{error}</li>
                ))}
              </ul>
            </div>
          ) : null}
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={create.isPending}
          >
            Cancel
          </Button>
          <Button
            type="button"
            disabled={errors.length > 0 || blockedPush || create.isPending}
            aria-busy={create.isPending}
            onClick={() => create.mutate()}
          >
            {create.isPending ? (
              <Loader2Icon className="animate-spin" aria-hidden="true" />
            ) : (
              <HammerIcon aria-hidden="true" />
            )}
            {create.isPending ? "Queueing…" : "Queue build"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ModeOption({
  checked,
  disabled,
  label,
  description,
  onChange,
}: {
  checked: boolean;
  disabled?: boolean;
  label: string;
  description: string;
  onChange: () => void;
}) {
  return (
    <label className="flex cursor-pointer items-start gap-3 rounded-md border border-border/70 p-3 has-[:checked]:border-primary/60 has-[:checked]:bg-primary/5 has-[:disabled]:cursor-not-allowed has-[:disabled]:opacity-50">
      <input
        type="radio"
        name="custom-build-destination"
        checked={checked}
        disabled={disabled}
        onChange={onChange}
        className="mt-0.5 size-4 accent-primary"
      />
      <span>
        <span className="block text-sm font-medium">{label}</span>
        <span className="mt-0.5 block text-xs text-muted-foreground">
          {description}
        </span>
      </span>
    </label>
  );
}
