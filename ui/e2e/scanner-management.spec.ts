import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Locator, type Page } from "@playwright/test";
import { installScannerApiMock, recorded } from "./scanner-api-mock";

const supplyChainBase = "/v1/scanner-supply-chain";
const scannerTabs = [
  ["overview", "Overview"],
  ["operations", "Operations"],
  ["updates", "Updates"],
  ["candidates", "Candidates"],
  ["releases", "Releases"],
  ["rollouts", "Rollouts"],
  ["policy", "Policy"],
  ["registries", "Registries"],
  ["custom_builds", "Custom builds"],
  ["signing", "Signing"],
  ["notifications", "Notifications"],
  ["audit", "Audit"],
] as const;

test.describe("human scanner persona administration", () => {
  test("assigns composable scanner personas from Settings Users", async ({
    page,
  }) => {
    const state = await installScannerApiMock(page);
    await page.goto("/settings?tab=users");

    await page
      .getByRole("button", {
        name: "Manage scanner access for operator@example.test",
      })
      .click();
    const dialog = page.getByRole("dialog", {
      name: "Scanner access for operator@example.test",
    });
    await expect(dialog).toBeVisible();
    const viewer = dialog.getByRole("checkbox", { name: /Viewer/ });
    const operator = dialog.getByRole("checkbox", {
      name: /Scanner operator/,
    });
    const approver = dialog.getByRole("checkbox", {
      name: /Release approver/,
    });
    await expect(viewer).toBeChecked();
    await operator.check();
    await approver.check();
    await expect(viewer).not.toBeChecked();
    await dialog.getByRole("button", { name: "Save scanner access" }).click();

    await expect
      .poll(
        () =>
          recorded(
            state,
            "PUT",
            "/users/user-operator/scanner-supply-chain-access",
          ).length,
      )
      .toBe(1);
    expect(
      recorded(
        state,
        "PUT",
        "/users/user-operator/scanner-supply-chain-access",
      )[0].body,
    ).toEqual({
      personas: ["scanner_operator", "release_approver"],
    });
    await expect(dialog).toBeHidden();
    await expect(
      page.getByText("Scanner operator", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText("Release approver", { exact: true }),
    ).toBeVisible();
  });

  test("applies a persisted persona revocation in the same browser session", async ({
    page,
  }) => {
    const state = await installScannerApiMock(page, {
      currentUser: {
        id: "user-operator",
        email: "operator@example.test",
        role: "user",
        scopes: ["read:scanner-supply-chain", "operate:scanner-supply-chain"],
        scanner_supply_chain_personas: ["scanner_operator"],
        scanner_supply_chain_scopes: [
          "read:scanner-supply-chain",
          "operate:scanner-supply-chain",
        ],
      },
    });
    await page.goto("/scanners?tab=overview");

    const checkNow = page.getByRole("button", { name: "Check now" });
    await expect(checkNow).toBeEnabled();
    await expect(page.getByRole("button", { name: "Registries" })).toHaveCount(
      0,
    );
    await expect(page.getByRole("button", { name: "Signing" })).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: "Custom builds" }),
    ).toHaveCount(0);

    state.currentUser.scopes = ["read:scanner-supply-chain"];
    state.currentUser.scanner_supply_chain_scopes = [
      "read:scanner-supply-chain",
    ];
    state.currentUser.scanner_supply_chain_personas = ["viewer"];
    await page.reload();

    await expect(page.getByText(/Read-only scanner access/)).toBeVisible();
    await expect(checkNow).toBeDisabled();
    await expect(page).toHaveURL(/\/scanners\?tab=overview/);
  });

  test("enforces all six human persona boundaries in scanner navigation and actions", async ({
    page,
  }) => {
    const state = await installScannerApiMock(page, {
      currentUser: {
        id: "persona-user",
        email: "persona@example.test",
        role: "user",
      },
    });
    const cases = [
      {
        persona: "viewer",
        scopes: ["read:scanner-supply-chain"],
        operate: false,
        approve: false,
        registries: false,
        administer: false,
      },
      {
        persona: "scanner_operator",
        scopes: ["read:scanner-supply-chain", "operate:scanner-supply-chain"],
        operate: true,
        approve: false,
        registries: false,
        administer: false,
      },
      {
        persona: "release_approver",
        scopes: ["read:scanner-supply-chain", "approve:scanner-releases"],
        operate: false,
        approve: true,
        registries: false,
        administer: false,
      },
      {
        persona: "registry_administrator",
        scopes: ["read:scanner-supply-chain", "manage:scanner-registries"],
        operate: false,
        approve: false,
        registries: true,
        administer: false,
      },
      {
        persona: "supply_chain_administrator",
        scopes: ["admin:scanner-supply-chain"],
        operate: true,
        approve: true,
        registries: true,
        administer: true,
      },
      {
        persona: "auditor",
        scopes: ["read:scanner-supply-chain"],
        operate: false,
        approve: false,
        registries: false,
        administer: false,
      },
    ] as const;

    for (const personaCase of cases) {
      state.currentUser.scopes = [...personaCase.scopes];
      state.currentUser.scanner_supply_chain_scopes = [...personaCase.scopes];
      state.currentUser.scanner_supply_chain_personas = [personaCase.persona];
      await page.goto("/scanners?tab=overview");

      if (personaCase.operate) {
        await expect(
          page.getByRole("button", { name: "Check now" }),
        ).toBeEnabled();
      } else {
        await expect(
          page.getByRole("button", { name: "Check now" }),
        ).toBeDisabled();
      }
      await expect(
        page.getByRole("button", { name: "Registries" }),
      ).toHaveCount(personaCase.registries ? 1 : 0);
      await expect(page.getByRole("button", { name: "Signing" })).toHaveCount(
        personaCase.administer ? 1 : 0,
      );
      await expect(
        page.getByRole("button", { name: "Custom builds" }),
      ).toHaveCount(0);
      await expect(page.getByRole("button", { name: "Audit" })).toHaveCount(1);

      await page.goto("/scanners?tab=registries");
      const addRegistry = page.getByRole("button", { name: "Add registry" });
      if (personaCase.registries) {
        await expect(page).toHaveURL(/\/scanners\?tab=registries/);
        await expect(addRegistry).toBeEnabled();
      } else {
        await expect(page).toHaveURL(/\/scanners\?tab=overview/);
        await expect(addRegistry).toHaveCount(0);
      }

      await page.goto("/scanners?tab=policy");
      const policyConfiguration = page.getByRole("group", {
        name: "Scanner release policy configuration",
      });
      const maximumStableImageAge = policyConfiguration.locator(
        'input[placeholder="168h0m0s"]',
      );
      if (personaCase.administer) {
        await expect(maximumStableImageAge).toBeEnabled();
      } else {
        await expect(maximumStableImageAge).toBeDisabled();
      }

      await page.goto("/scanners?tab=candidates&candidate=candidate-1");
      const approve = page.getByRole("button", {
        name: "Approve",
        exact: true,
      });
      if (personaCase.approve) {
        await expect(approve).toBeEnabled();
      } else {
        await expect(approve).toBeDisabled();
      }
    }
  });
});

test.describe("scanner settings and image inventory", () => {
  test("keeps existing scanner controls functional @visual", async ({
    page,
  }) => {
    const state = await installScannerApiMock(page);
    await page.goto("/settings?tab=scanners");

    await expect(
      page.getByRole("heading", { level: 1, name: "Settings" }),
    ).toBeVisible();
    const settingsNavigation = page.getByRole("navigation", {
      name: "Administration settings",
    });
    await expect(
      settingsNavigation.getByRole("button", {
        name: "Scanners",
        exact: true,
      }),
    ).toHaveAttribute("aria-current", "page");
    await expectActiveControlWithinScrollableNavigation(settingsNavigation);
    await expectNoPageOverflow(page);
    await expect(
      page.getByRole("heading", { name: "Scanner release management" }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Set up scanners (pull images)" }),
    ).toBeEnabled();
    await expect(
      page.getByRole("button", { name: "Rebuild (local)" }),
    ).toHaveCount(4);
    await expect(
      page.getByRole("heading", { name: "Scanner tools" }),
    ).toBeVisible();
    await expect(page.getByText("Semgrep", { exact: true })).toBeVisible();
    await expect(page.getByText("Trivy", { exact: true })).toBeVisible();

    await page.getByRole("button", { name: "Run Doctor" }).click();
    await expect(page.getByText("All checks passed")).toBeVisible();

    const updateImage = page.getByRole("button", {
      name: "Update",
      exact: true,
    });
    await expect(updateImage).toHaveCount(1);
    await updateImage.click();
    await expect
      .poll(() => recorded(state, "POST", "/scanners/images/pull").length, {
        message: "the existing per-image update control should call its API",
      })
      .toBe(1);
    expect(recorded(state, "POST", "/scanners/images/pull")[0].body).toEqual({
      image: "docker.io/alphabravodevops/wolf-scanners:2.0.0",
    });
    await page
      .getByRole("button", { name: "Set up scanners (pull images)" })
      .click();
    await expect
      .poll(() => recorded(state, "POST", "/scanners/pull").length, {
        message: "the existing scanner image setup control should call its API",
      })
      .toBe(1);
    await expect(page.getByText("Pulled 4 images")).toBeVisible();

    await expectSemanticPage(page);
    await expectAutomatedAccessibility(page);
    await settleVisuals(page);
    await expect(page.locator("main")).toHaveScreenshot(
      "scanner-settings-inventory.png",
    );

    const codeqlRow = page.locator("li").filter({
      has: page.getByText("CodeQL", { exact: true }),
    });
    await codeqlRow.getByRole("button", { name: "Rebuild (local)" }).click();
    const buildDialog = page.getByRole("dialog", {
      name: "Queue durable custom build",
    });
    await expect(
      buildDialog.getByText("CodeQL is local-only.", { exact: false }),
    ).toBeVisible();
    await buildDialog
      .getByLabel("Reason")
      .fill("Refresh the local licensed CodeQL toolchain");
    await buildDialog.getByRole("button", { name: "Queue build" }).click();
    await expect
      .poll(() => recorded(state, "POST", "/v1/scanners/custom-builds").length)
      .toBe(1);
    expect(
      recorded(state, "POST", "/v1/scanners/custom-builds")[0].body,
    ).toEqual({
      variants: ["codeql"],
      push: false,
      platforms: ["linux/amd64"],
      reason: "Refresh the local licensed CodeQL toolchain",
    });
    await expect(page.getByText("Durable custom build queued")).toBeVisible();
    await expect(
      page.getByRole("link", { name: "Open Custom Build" }),
    ).toHaveAttribute(
      "href",
      "/scanners?tab=custom_builds&custom_build=custom-build-codeql-local&custom_build_operation_id=op_custom_build_0003&custom_build_trace_id=0123456789abcdef0123456789abcdef",
    );
    await expect(
      page.getByRole("link", { name: "Open operation audit" }),
    ).toHaveAttribute(
      "href",
      "/scanners?tab=audit&operation_id=op_custom_build_0003&trace_id=0123456789abcdef0123456789abcdef",
    );
  });

  test("renders the Kubernetes-managed capability branch without Docker actions", async ({
    page,
  }) => {
    await installScannerApiMock(page, { scanRuntime: "kubernetes" });
    await page.goto("/settings?tab=scanners");

    await expect(
      page.getByRole("heading", {
        name: "Scanner runtime is managed by Kubernetes",
      }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Set up scanners (pull images)" }),
    ).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Run Doctor" })).toHaveCount(
      0,
    );
    await page
      .getByRole("button", { name: "Open supply-chain console" })
      .click();
    await expect(page).toHaveURL(/\/scanners/);
    await expectNoPageOverflow(page);
    await expectAutomatedAccessibility(page);
  });
});

test.describe("ordinary remote scan workflow", () => {
  test("starts an API-backed repository scan, renders partial results, and cancels safely", async ({
    page,
  }) => {
    const state = await installScannerApiMock(page);
    await page.goto("/scans");

    await page.getByRole("button", { name: "New scan" }).click();
    await page.getByLabel("Repo *").selectOption("repo-api-created");
    await page.getByRole("button", { name: "Pick tools" }).click();
    await page.getByLabel("semgrep").check();
    await page.getByRole("button", { name: "Start scan (1)" }).click();

    await expect(page).toHaveURL(/\/scans\/scan-api-created/);
    await expect(
      page.getByRole("heading", { name: /API imported repository/ }),
    ).toBeVisible();
    await expect(
      page.getByRole("link", { name: "Command injection from request input" }),
    ).toBeVisible();
    expect(recorded(state, "POST", "/scans")[0].body).toEqual({
      repo_id: "repo-api-created",
      branch: "main",
      tools: ["semgrep"],
    });
    await expectSemanticPage(page);
    await expectAutomatedAccessibility(page);
    await expectNoPageOverflow(page);

    await page.getByRole("button", { name: "Cancel scan" }).click();
    const confirmCancel = page.getByRole("button", { name: "Confirm cancel" });
    await expect(confirmCancel).toBeFocused();
    await confirmCancel.click();
    await expect(
      page.getByRole("status").filter({ hasText: "Scan cancelled." }),
    ).toBeVisible();
    expect(recorded(state, "DELETE", "/scans/scan-api-created")).toHaveLength(
      1,
    );
  });
});

test.describe("application shell accessibility", () => {
  test("offers a keyboard skip link with a focusable main target", async ({
    page,
  }) => {
    await installScannerApiMock(page);
    await page.goto("/scanners?tab=overview");

    const skipLink = page.getByRole("link", { name: "Skip to main content" });
    await tabTo(page, skipLink);
    await expect(skipLink).toBeFocused();
    await expect(skipLink).toBeVisible();
    await page.keyboard.press("Enter");
    await expect(page.locator("#main-content")).toBeFocused();
  });

  test("keeps the mobile navigation modal, trapped, and dismissible", async ({
    page,
  }, testInfo) => {
    test.skip(
      !testInfo.project.name.startsWith("mobile-chromium-"),
      "The drawer exists only below the desktop breakpoint.",
    );
    await installScannerApiMock(page);
    await page.goto("/scanners?tab=overview");

    const openMenu = page.getByRole("button", { name: "Open menu" });
    await expect(openMenu).toHaveAttribute("aria-expanded", "false");
    await openMenu.click();

    const drawer = page.getByRole("dialog", { name: "Primary navigation" });
    await expect(drawer).toHaveAttribute("aria-modal", "true");
    await expect(
      page.getByRole("button", { name: "Close menu" }),
    ).toBeFocused();
    await expectDialogFocusTrap(page, drawer);
    await expectAutomatedAccessibility(page, "body");

    await page.keyboard.press("Escape");
    await expect(openMenu).toBeFocused();
    await expect(openMenu).toHaveAttribute("aria-expanded", "false");
    const closedDrawer = page.locator("#primary-navigation");
    await expect(closedDrawer).toHaveAttribute("aria-hidden", "true");
    expect(
      await closedDrawer.evaluate((element) => (element as HTMLElement).inert),
    ).toBe(true);

    await openMenu.click();
    await page
      .getByRole("button", { name: "Close navigation menu" })
      .click({ position: { x: 350, y: 200 } });
    await expect(openMenu).toBeFocused();
    await expect(openMenu).toHaveAttribute("aria-expanded", "false");
  });

  test("honors reduced motion and synchronizes browser chrome with the theme", async ({
    page,
  }, testInfo) => {
    test.skip(
      !testInfo.project.name.startsWith("desktop-chromium-"),
      "Motion and theme behavior is viewport-independent.",
    );
    await installScannerApiMock(page);
    await page.goto("/scanners?tab=overview");

    const initialTheme = await page.evaluate(() => {
      const probe = document.createElement("div");
      probe.className = "animate-spin transition-transform";
      document.body.append(probe);
      const style = getComputedStyle(probe);
      const durationToMs = (duration: string) =>
        duration.endsWith("ms")
          ? Number.parseFloat(duration)
          : Number.parseFloat(duration) * 1_000;
      const result = {
        colorScheme: getComputedStyle(document.documentElement).colorScheme,
        metaColor: document
          .querySelector<HTMLMetaElement>('meta[name="theme-color"]')
          ?.getAttribute("content"),
        animationMs: durationToMs(style.animationDuration),
        transitionMs: durationToMs(style.transitionDuration),
      };
      probe.remove();
      return result;
    });
    expect(initialTheme).toMatchObject({
      colorScheme: "dark",
      metaColor: "#0a0c10",
    });
    expect(initialTheme.animationMs).toBeLessThanOrEqual(0.01);
    expect(initialTheme.transitionMs).toBeLessThanOrEqual(0.01);

    await page.getByRole("button", { name: "Switch to light mode" }).click();
    await expect(page.locator("html")).toHaveClass(/light/);
    await expect
      .poll(() =>
        page.locator('meta[name="theme-color"]').getAttribute("content"),
      )
      .toBe("#f8f9fb");
    expect(
      await page.evaluate(
        () => getComputedStyle(document.documentElement).colorScheme,
      ),
    ).toBe("light");

    await page.getByRole("button", { name: "Switch to dark mode" }).click();
    await expect(page.locator("html")).toHaveClass(/dark/);
    await expect
      .poll(() =>
        page.locator('meta[name="theme-color"]').getAttribute("content"),
      )
      .toBe("#0a0c10");
  });
});

test.describe("scanner release management", () => {
  test("distinguishes synthetic corpus currentness from sampled real-scan health", async ({
    page,
  }) => {
    await installScannerApiMock(page);
    await page.goto("/scanners?tab=rollouts&rollout=rollout-1");

    const synthetic = page.getByRole("region", {
      name: "Synthetic fixture verification",
    });
    await expect(synthetic).toContainText(
      "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    );
    await expect(synthetic.getByLabel("Status: Stale")).toBeVisible();
    await expect(synthetic).toContainText("24/24");
    await expect(synthetic).toContainText(
      "stale for the current approved corpus",
    );

    const realScans = page.getByRole("region", {
      name: "Sampled real-scan health",
    });
    await expect(realScans.getByLabel("Status: Degraded")).toBeVisible();
    await expect(realScans).toContainText("Candidate samples");
    await expect(realScans).toContainText("12");
    await expect(realScans).toContainText("Stable samples");
    await expect(realScans).toContainText("30");
    await expect(realScans).toContainText("Expected finding losses");
    await expect(realScans).toContainText("+24,000 ms");
    await expect(
      page.getByText("synthetic-corpus-internal-id-never-render"),
    ).toHaveCount(0);
    await expect(page.getByText("Verification scans")).toHaveCount(0);
    await expectNoPageOverflow(page);
    await expectAutomatedAccessibility(page);
  });

  test("preserves a partial all-build through reload and renders safe per-variant remediation", async ({
    page,
  }) => {
    await installScannerApiMock(page);
    await page.goto(
      "/scanners?tab=custom_builds&custom_build=custom-build-partial-all",
    );

    await expect(
      page.getByRole("heading", { level: 1, name: "Custom build operation" }),
    ).toBeVisible();
    await expect(
      page.getByText("Some variants did not complete"),
    ).toBeVisible();
    await expect(page.getByText("sha256:custom-default")).toBeVisible();
    await expect(page.getByText("sha256:custom-jvm")).toBeVisible();
    await expect(page.getByText("sha256:custom-rust")).toBeVisible();
    await expect(page.getByRole("heading", { name: "CodeQL" })).toBeVisible();
    await expect(
      page.getByText("dckr_pat_build_error_never_render"),
    ).toHaveCount(0);
    await expect(page.getByText("dckr_pat_variant_never_render")).toHaveCount(
      0,
    );
    await expect(page.getByText("idempotency-never-render")).toHaveCount(0);
    await expect(page.getByText("dckr_pat_terminal_never_render")).toHaveCount(
      0,
    );

    await page.reload();
    await expect(page).toHaveURL(
      /tab=custom_builds.*custom_build=custom-build-partial-all/,
    );
    await expect(page.getByText("sha256:custom-default")).toBeVisible();
    await expectNoPageOverflow(page);
    await expectAutomatedAccessibility(page);
  });

  test("resumes running logs with Last-Event-ID and keeps the deep link on reload", async ({
    page,
  }) => {
    const state = await installScannerApiMock(page);
    await page.goto(
      "/scanners?tab=custom_builds&custom_build=custom-build-running",
    );

    await expect(
      page.getByRole("log", { name: "Custom build log" }),
    ).toContainText("durable log 1");
    await expect
      .poll(
        () =>
          state.customBuildEventRequests.some(
            (request) => request.lastEventId === "1",
          ),
        { message: "the reconnect should resume after log sequence 1" },
      )
      .toBe(true);
    await expect(
      page.getByRole("log", { name: "Custom build log" }),
    ).toContainText("durable log 2");

    await page.reload();
    await expect(page).toHaveURL(
      /tab=custom_builds.*custom_build=custom-build-running/,
    );
    await expect(
      page.getByRole("log", { name: "Custom build log" }),
    ).toContainText("durable log 1");
  });

  test("blocks push-all, queues a supported push, and uses guarded cancel/retry", async ({
    page,
  }) => {
    const state = await installScannerApiMock(page);
    await page.goto("/scanners?tab=custom_builds");
    await page
      .getByRole("button", { name: "Queue custom build" })
      .first()
      .click();

    let dialog = page.getByRole("dialog", {
      name: "Queue durable custom build",
    });
    await dialog.getByRole("radio", { name: /Push to registry/ }).click();
    await dialog.getByLabel("Reason").fill("Publish refreshed scanners");
    await expect(
      dialog.getByText(
        /Build all cannot be pushed because CodeQL is local-only/,
      ),
    ).toBeVisible();
    await expect(
      dialog.getByRole("button", { name: "Queue build" }),
    ).toBeDisabled();
    expect(recorded(state, "POST", "/v1/scanners/custom-builds")).toHaveLength(
      0,
    );

    await dialog.getByLabel("Scanner variant").selectOption("default");
    await dialog.getByText("linux/arm64", { exact: true }).click();
    await dialog.getByRole("button", { name: "Queue build" }).click();
    await expect
      .poll(() => recorded(state, "POST", "/v1/scanners/custom-builds").length)
      .toBe(1);
    expect(
      recorded(state, "POST", "/v1/scanners/custom-builds")[0].body,
    ).toEqual({
      variants: ["default"],
      push: true,
      platforms: ["linux/amd64", "linux/arm64"],
      reason: "Publish refreshed scanners",
    });
    await expect(page).toHaveURL(
      /custom_build_operation_id=op_custom_build_0003/,
    );
    await expect(
      page.getByRole("link", { name: "Open operation audit" }),
    ).toHaveAttribute(
      "href",
      "/scanners?tab=audit&operation_id=op_custom_build_0003&trace_id=0123456789abcdef0123456789abcdef",
    );

    await page.goto(
      "/scanners?tab=custom_builds&custom_build=custom-build-running",
    );
    await page.getByRole("button", { name: "Cancel build" }).click();
    dialog = page.getByRole("dialog", { name: "Cancel custom build" });
    await dialog.getByLabel("Reason").fill("Maintenance window closed");
    await dialog
      .getByLabel("Type custom-build-running to confirm")
      .fill("custom-build-running");
    await dialog.getByRole("button", { name: "Request cancellation" }).click();
    await expect
      .poll(
        () =>
          recorded(
            state,
            "POST",
            "/v1/scanners/custom-builds/custom-build-running/cancel",
          ).length,
      )
      .toBe(1);
    const cancelRequest = recorded(
      state,
      "POST",
      "/v1/scanners/custom-builds/custom-build-running/cancel",
    )[0];
    expect(cancelRequest.body).toEqual({ reason: "Maintenance window closed" });
    expect(cancelRequest.headers?.["if-match"]).toBe('"2"');
    expect(cancelRequest.headers?.["idempotency-key"]).toMatch(/^wolf-ui-/);

    await page.goto(
      "/scanners?tab=custom_builds&custom_build=custom-build-partial-all",
    );
    await page.getByRole("button", { name: "Retry" }).click();
    dialog = page.getByRole("dialog", { name: "Retry custom build" });
    await dialog.getByLabel("Reason").fill("Buildx capacity restored");
    await dialog
      .getByLabel("Type custom-build-partial-all to confirm")
      .fill("custom-build-partial-all");
    await dialog.getByRole("button", { name: "Retry build" }).click();
    await expect
      .poll(
        () =>
          recorded(
            state,
            "POST",
            "/v1/scanners/custom-builds/custom-build-partial-all/retry",
          ).length,
      )
      .toBe(1);
  });

  test("qualifies every top-level panel for names, WCAG AA axe rules, and responsive containment @browser-matrix", async ({
    page,
  }) => {
    await installScannerApiMock(page);

    for (const [tab, label] of scannerTabs) {
      await page.goto(`/scanners?tab=${tab}`);
      const navigation = page.getByRole("navigation", {
        name: "Scanner release management",
      });
      await expect(
        navigation.getByRole("button", { name: label, exact: true }),
      ).toHaveAttribute("aria-current", "page");
      await expectActiveControlWithinScrollableNavigation(navigation);
      await expect(page.locator("main h1")).toBeVisible();
      await expectSemanticPage(page);
      await expectAutomatedAccessibility(page);
      await expectNoPageOverflow(page);
    }
  });

  test("qualifies every top-level panel in the light theme @browser-matrix", async ({
    page,
  }) => {
    await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
    await page.addInitScript(() => {
      window.localStorage.setItem("theme", "light");
    });
    await installScannerApiMock(page);

    for (const [tab, label] of scannerTabs) {
      await page.goto(`/scanners?tab=${tab}`);
      await expect(page.locator("html")).toHaveClass(/light/);
      const navigation = page.getByRole("navigation", {
        name: "Scanner release management",
      });
      await expect(
        navigation.getByRole("button", { name: label, exact: true }),
      ).toHaveAttribute("aria-current", "page");
      await expectActiveControlWithinScrollableNavigation(navigation);
      await expectSemanticPage(page);
      await expectAutomatedAccessibility(page);
      await expectNoPageOverflow(page);
    }
  });

  test("deep-links critical overview health to bounded alert remediation", async ({
    page,
  }) => {
    await installScannerApiMock(page);
    await page.goto("/scanners?tab=overview");

    const critical = page.getByRole("alert").filter({
      hasText: "1 open critical scanner alert",
    });
    const remediation = critical.getByRole("link", {
      name: "Review critical alerts",
    });
    await expect(remediation).toHaveAttribute(
      "href",
      "/scanners?tab=notifications&notification_view=alerts",
    );
    await expect(page.getByText("dckr_pat_alert_never_render")).toHaveCount(0);
    await remediation.click();
    await expect(page).toHaveURL(/tab=notifications.*notification_view=alerts/);
    await expect(
      page.getByRole("heading", { level: 1, name: "Scanner alerts" }),
    ).toBeVisible();
  });

  test("announces loading and recovers from a retryable overview failure", async ({
    page,
  }, testInfo) => {
    test.skip(
      !testInfo.project.name.startsWith("desktop-chromium-"),
      "Loading and retry semantics are viewport-independent; both viewports exercise the recovered overview in the panel matrix.",
    );
    await installScannerApiMock(page, {
      delayGetMsByPath: {
        [`${supplyChainBase}/overview`]: 300,
      },
      failGetCountByPath: {
        [`${supplyChainBase}/overview`]: 2,
      },
    });
    await page.goto("/scanners?tab=overview");

    await expect(
      page.getByRole("status", {
        name: "Loading scanner release data",
      }),
    ).toBeVisible();
    await expect(
      page.getByRole("alert").filter({
        hasText: "Scanner release-management state is unavailable",
      }),
    ).toBeVisible({ timeout: 10_000 });
    const dataError = page.getByRole("alert").filter({
      hasText: "Release management unavailable",
    });
    await expect(dataError).toBeVisible();
    await dataError.getByRole("button", { name: "Retry" }).click();
    await expect(page.getByText("1 open critical scanner alert")).toBeVisible();
    await expect(dataError).toBeHidden();
  });

  test("activates every scanner panel with the keyboard and announces the result", async ({
    page,
  }, testInfo) => {
    test.skip(
      !testInfo.project.name.startsWith("desktop-chromium-"),
      "The horizontal navigation uses the same DOM and focus order at mobile widths.",
    );
    await installScannerApiMock(page);
    await page.goto("/scanners?tab=overview");
    const navigation = page.getByRole("navigation", {
      name: "Scanner release management",
    });

    for (const [tab, label] of scannerTabs) {
      const control = navigation.getByRole("button", {
        name: label,
        exact: true,
      });
      await tabTo(page, control);
      await expectFocusIndicator(control);
      await page.keyboard.press("Enter");
      await expect(control).toHaveAttribute("aria-current", "page");
      await expect(
        page.getByText(`Showing ${label} scanner release panel`, {
          exact: true,
        }),
      ).toBeAttached();
      await expect(page).toHaveURL(new RegExp(`tab=${tab}`));
    }
  });

  test("creates an explicit new-release re-scan without rewriting ordinary retry provenance", async ({
    page,
  }) => {
    const state = await installScannerApiMock(page);
    await page.goto("/scans/scan-source");

    await expect(
      page.getByRole("heading", { level: 1, name: /payments-service/ }),
    ).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "Scanner release provenance" }),
    ).toBeVisible();
    await expect(page.getByText("release-stable")).toBeVisible();
    await expect(page.getByText("sha256:manifest-stable")).toBeVisible();

    const action = page.getByRole("button", {
      name: "Re-scan under different release",
    });
    await expect(action).toBeEnabled();
    await action.click();

    const dialog = page.getByRole("dialog", {
      name: "Create a distinct release re-scan?",
    });
    await expectFocusWithin(dialog);
    await expect(dialog.getByText("This is not Retry.")).toBeVisible();
    await expect(dialog).toContainText(
      "Ordinary retry continues scan scan-source under its pinned release",
    );
    await expect(dialog.getByLabel("Immutable scanner release")).toHaveValue(
      "release-next",
    );
    await dialog
      .getByLabel("Reason")
      .fill("Compare findings under the approved scanner release");
    await dialog
      .getByLabel("Type release-next to confirm")
      .fill("release-next");
    await expectAutomatedAccessibility(page, '[role="dialog"]');
    await dialog
      .getByRole("button", { name: "Create distinct re-scan" })
      .click();

    const successDialog = page.getByRole("dialog", {
      name: "Distinct re-scan created",
    });
    await expect(successDialog).toBeVisible();
    await expect(
      successDialog.getByRole("link", { name: "Open new scan" }),
    ).toHaveAttribute("href", "/scans/scan-release-rescan");
    const requests = recorded(
      state,
      "POST",
      "/scans/scan-source/release-rescans",
    );
    expect(requests).toHaveLength(1);
    expect(requests[0].body).toEqual({
      release_id: "release-next",
      reason: "Compare findings under the approved scanner release",
    });
    expect(requests[0].headers?.["idempotency-key"]).toMatch(/^wolf-ui-/);
  });

  test("imports current scanner configuration as guarded legacy evidence", async ({
    page,
  }) => {
    const state = await installScannerApiMock(page);
    await page.goto("/scanners?tab=releases");

    const open = page.getByRole("button", {
      name: "Import legacy snapshot",
    });
    await expect(open).toBeEnabled();
    await open.click();

    const dialog = page.getByRole("dialog", {
      name: "Import legacy configuration snapshot",
    });
    await expectFocusWithin(dialog);
    await expect(dialog).toContainText(
      "Evidence-only import with permanent limitations",
    );
    await expect(dialog).toContainText(
      "does not change the desired release, worker assignments, or queued and running scans",
    );
    await expect(
      dialog.getByRole("heading", { name: "Configuration preview" }),
    ).toBeVisible();
    await dialog
      .getByLabel("Digest for default")
      .fill(`sha256:${"a".repeat(64)}`);
    await dialog
      .getByLabel("Digest for wolf-semgrep")
      .fill(`sha256:${"b".repeat(64)}`);
    await dialog
      .getByLabel("Audit reason")
      .fill("Archive deployment state before enabling managed releases");
    await dialog
      .getByLabel("Type IMPORT LEGACY SNAPSHOT to confirm")
      .fill("IMPORT LEGACY SNAPSHOT");
    await expectAutomatedAccessibility(page, '[role="dialog"]');
    await dialog
      .getByRole("button", { name: "Import evidence snapshot" })
      .click();

    const successDialog = page.getByRole("dialog", {
      name: "Legacy snapshot imported",
    });
    await expect(successDialog).toBeVisible();
    await expect(successDialog.getByText(/Runtime unchanged/)).toBeVisible();
    await expect(
      successDialog.getByText(/Not rollback eligible/),
    ).toBeVisible();
    await expect(
      successDialog.getByRole("link", { name: "Open imported release" }),
    ).toHaveAttribute(
      "href",
      "/scanners?tab=releases&release=legacy-release-config",
    );
    const requests = recorded(
      state,
      "POST",
      `${supplyChainBase}/legacy-release-imports`,
    );
    expect(requests).toHaveLength(1);
    expect(requests[0].body).toEqual({
      reason: "Archive deployment state before enabling managed releases",
      resolved_digests: {
        default: `sha256:${"a".repeat(64)}`,
        "wolf-semgrep": `sha256:${"b".repeat(64)}`,
      },
    });
    expect(requests[0].headers?.["idempotency-key"]).toMatch(/^wolf-ui-/);
  });

  test("administers masked signer profiles through create, rotate, and revoke", async ({
    page,
  }) => {
    const state = await installScannerApiMock(page);
    await page.goto("/scanners?tab=signing");

    await expect(
      page.getByRole("heading", { level: 1, name: "Signing profiles" }),
    ).toBeVisible();
    await expect(
      page.getByText("No private key material crosses this control plane"),
    ).toBeVisible();
    await expect(page.getByText("Wolf managed keyless")).toBeVisible();

    await page
      .getByRole("row", { name: "Open signer Wolf managed keyless" })
      .click();
    await expect(
      page.getByText("Deployment-owned managed keyless profile"),
    ).toBeVisible();
    await expect(page.getByRole("button", { name: "Rotate" })).toBeDisabled();
    await expect(page.getByRole("button", { name: "Revoke" })).toBeDisabled();
    await page.getByRole("button", { name: "All signing profiles" }).click();

    const createSigner = page.getByRole("button", {
      name: "Create signing profile",
    });
    await createSigner.click();
    const createDialog = page.getByRole("dialog", {
      name: "Create signing profile",
    });
    await expect(createDialog.getByLabel("Profile name")).toBeFocused();
    await expectDialogFocusTrap(page, createDialog);
    await createDialog.getByLabel("Provider").selectOption("gcp_kms");
    await createDialog.getByLabel("Profile name").fill("GCP release signer");
    const createdKey =
      "gcp-kms://projects/wolf/locations/global/keyRings/release/cryptoKeys/prod";
    await createDialog.getByLabel("Opaque key reference").fill(createdKey);
    await createDialog
      .getByLabel("Bound identity")
      .fill("release-signer@wolf-prod.iam.gserviceaccount.com");
    await createDialog
      .getByLabel("Issuer URI")
      .fill("https://accounts.google.com");
    await createDialog
      .getByLabel("Bound subject")
      .fill("release-signer@wolf-prod.iam.gserviceaccount.com");
    await createDialog
      .getByLabel("Opaque trust-root policy reference")
      .fill("kubernetes://wolf-system/gcp-kms-roots");
    await expectAutomatedAccessibility(page, '[role="dialog"]');
    await createDialog.getByRole("button", { name: "Create profile" }).click();

    await expect(page).toHaveURL(/tab=signing.*signer=signer-created/);
    await expect(
      page.getByRole("heading", { level: 1, name: "GCP release signer" }),
    ).toBeVisible();
    await expect(page.getByText("gcp-kms://***")).toBeVisible();
    await expect(page.getByText(createdKey)).toHaveCount(0);
    await expect(page.getByText("kubernetes://***")).toBeVisible();

    await page.getByRole("button", { name: "Rotate" }).click();
    const rotateDialog = page.getByRole("dialog", {
      name: "Rotate signing profile",
    });
    await expect(rotateDialog.getByLabel("Profile name")).toBeFocused();
    const rotatedKey =
      "gcp-kms://projects/wolf/locations/global/keyRings/release/cryptoKeys/prod-v2";
    await rotateDialog.getByLabel("Opaque key reference").fill(rotatedKey);
    await rotateDialog
      .getByLabel("Opaque trust-root policy reference")
      .fill("kubernetes://wolf-system/gcp-kms-roots-v2");
    await rotateDialog
      .getByLabel("Type signer-created to confirm rotation")
      .fill("signer-created");
    await expectAutomatedAccessibility(page, '[role="dialog"]');
    await rotateDialog.getByRole("button", { name: "Rotate profile" }).click();

    await expect(page).toHaveURL(/tab=signing.*signer=signer-rotated/);
    await expect(
      page.getByText("signer-created", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText("signer-rotated", { exact: true }),
    ).toBeVisible();
    await expect(page.getByText(rotatedKey)).toHaveCount(0);

    await page.getByRole("button", { name: "Revoke" }).click();
    const revokeDialog = page.getByRole("dialog", {
      name: "Revoke signing profile?",
    });
    await expect(revokeDialog.getByLabel("Revocation reason")).toBeFocused();
    await expect(revokeDialog).toContainText("This is not deletion.");
    await revokeDialog
      .getByLabel("Revocation reason")
      .fill("Key version retired after verified rotation");
    await revokeDialog
      .getByLabel("Type signer-rotated to confirm revocation")
      .fill("signer-rotated");
    await expectAutomatedAccessibility(page, '[role="dialog"]');
    await revokeDialog.getByRole("button", { name: "Revoke profile" }).click();

    await expect(page.getByText("Revoked signer profile")).toBeVisible();
    await expect(page.getByLabel("Status: Revoked").first()).toBeVisible();
    await expect(page.getByText(/Key version retired/)).toBeVisible();
    await expectSemanticPage(page);
    await expectAutomatedAccessibility(page);

    const createRequests = recorded(
      state,
      "POST",
      `${supplyChainBase}/signers`,
    );
    expect(createRequests).toHaveLength(1);
    expect(createRequests[0].body).toEqual({
      name: "GCP release signer",
      provider: "gcp_kms",
      algorithm: "ed25519",
      key_reference: createdKey,
      workload_identity: true,
      identity: "release-signer@wolf-prod.iam.gserviceaccount.com",
      issuer: "https://accounts.google.com",
      subject: "release-signer@wolf-prod.iam.gserviceaccount.com",
      trust_root_reference: "kubernetes://wolf-system/gcp-kms-roots",
    });
    expect(createRequests[0].headers?.["idempotency-key"]).toMatch(/^wolf-ui-/);

    const rotateRequests = recorded(
      state,
      "POST",
      `${supplyChainBase}/signers/signer-created/rotate`,
    );
    expect(rotateRequests).toHaveLength(1);
    expect(rotateRequests[0].body).toMatchObject({
      provider: "gcp_kms",
      key_reference: rotatedKey,
      trust_root_reference: "kubernetes://wolf-system/gcp-kms-roots-v2",
    });
    expect(rotateRequests[0].headers?.["if-match"]).toBe('"1"');
    expect(rotateRequests[0].headers?.["idempotency-key"]).toMatch(/^wolf-ui-/);

    const revokeRequests = recorded(
      state,
      "POST",
      `${supplyChainBase}/signers/signer-rotated/revoke`,
    );
    expect(revokeRequests).toHaveLength(1);
    expect(revokeRequests[0].body).toEqual({
      reason: "Key version retired after verified rotation",
    });
    expect(revokeRequests[0].headers?.["if-match"]).toBe('"2"');
    expect(revokeRequests[0].headers?.["idempotency-key"]).toMatch(/^wolf-ui-/);
  });

  test("runs on-demand discovery through exception, approval, publication, and rollout", async ({
    page,
  }, testInfo) => {
    test.skip(
      !testInfo.project.name.startsWith("desktop-chromium-"),
      "The stateful command journey is covered once; responsive rendering is covered separately.",
    );
    const state = await installScannerApiMock(page);
    await page.goto("/scanners?tab=updates");

    await expect(
      page.getByRole("heading", { level: 1, name: "Scanner updates" }),
    ).toBeVisible();
    await page.getByRole("button", { name: "Check all now" }).click();
    await expect
      .poll(
        () =>
          recorded(state, "POST", `${supplyChainBase}/discovery-runs`).length,
      )
      .toBe(1);
    expect(
      recorded(state, "POST", `${supplyChainBase}/discovery-runs`)[0].body,
    ).toEqual({
      scope: { type: "all" },
      reason: "On-demand complete scanner update discovery",
    });

    await page.getByRole("checkbox", { name: "Select semgrep" }).check();
    await page.getByRole("button", { name: "Create candidate (1)" }).click();
    await expect(page).toHaveURL(/tab=candidates.*candidate=candidate-1/);
    await expect(
      page.getByRole("heading", { level: 1, name: "candidate-1" }),
    ).toBeVisible();
    expect(
      recorded(state, "POST", `${supplyChainBase}/candidates`)[0].body,
    ).toEqual({
      selected_items: ["update-semgrep"],
      reason: "Candidate created from 1 selected update",
      discovery_run_id: "discovery-previous",
    });

    await expect(
      page.getByText("dckr_pat_candidate_summary_never_render"),
    ).toHaveCount(0);
    await expect(page.locator('a[href^="javascript:"]')).toHaveCount(0);
    await expect(page.getByRole("link", { name: "Evidence" })).toHaveAttribute(
      "href",
      "https://github.com/example/wolf/actions/runs/1234",
    );

    await page.getByRole("button", { name: "Record exception" }).click();
    const exceptionDialog = page.getByRole("dialog", {
      name: "Record Vulnerability gate exception?",
    });
    await exceptionDialog
      .getByLabel("Compensating-control owner")
      .fill("team-security-platform");
    await exceptionDialog
      .getByLabel("Exception reason")
      .fill("Bounded upstream vulnerability exposure");
    await exceptionDialog
      .getByLabel("Compensating control")
      .fill("Network isolation, active monitoring, and immediate rollback");
    await exceptionDialog
      .getByRole("button", { name: "Record exception" })
      .click();
    await expect
      .poll(
        () =>
          recorded(
            state,
            "POST",
            `${supplyChainBase}/candidates/candidate-1/exceptions`,
          ).length,
      )
      .toBe(1);
    const exceptionRequest = recorded(
      state,
      "POST",
      `${supplyChainBase}/candidates/candidate-1/exceptions`,
    )[0];
    expect(exceptionRequest.body).toMatchObject({
      gate: "vulnerability",
      owner_id: "team-security-platform",
      reason: "Bounded upstream vulnerability exposure",
      compensating_control:
        "Network isolation, active monitoring, and immediate rollback",
      evidence_digest: `sha256:${"a".repeat(64)}`,
    });
    expect(
      new Date(
        String((exceptionRequest.body as { expires_at: string }).expires_at),
      ).getTime(),
    ).toBeGreaterThan(Date.now());
    expect(exceptionRequest.headers?.["idempotency-key"]).toMatch(/^wolf-ui-/);

    await page.getByRole("tab", { name: "Approvals" }).click();
    await expect(page.getByText("team-security-platform")).toBeVisible();
    await expect(
      page.getByText(
        "Network isolation, active monitoring, and immediate rollback",
      ),
    ).toBeVisible();

    await page.getByRole("button", { name: "Approve" }).click();
    const approvalDialog = page.getByRole("dialog", {
      name: "Approve candidate?",
    });
    await expect(approvalDialog).toBeVisible();
    await approvalDialog
      .getByLabel("Reason")
      .fill("Reviewed compatibility, signatures, and provenance");
    await approvalDialog.getByRole("button", { name: "Approve" }).click();
    await expect
      .poll(
        () =>
          recorded(
            state,
            "POST",
            `${supplyChainBase}/candidates/candidate-1/approve`,
          ).length,
      )
      .toBe(1);
    await expect(page.getByRole("button", { name: "Publish" })).toBeEnabled();
    const approve = recorded(
      state,
      "POST",
      `${supplyChainBase}/candidates/candidate-1/approve`,
    )[0];
    expect(approve.body).toEqual({
      reason: "Reviewed compatibility, signatures, and provenance",
      lock_digest: "sha256:lock0001",
      policy_decision_digest: `sha256:${"d".repeat(64)}`,
      evidence_digest: `sha256:${"e".repeat(64)}`,
    });
    expect(approve.headers?.["if-match"]).toBe("3");
    expect(approve.headers?.["idempotency-key"]).toMatch(/^wolf-ui-/);

    await page.getByRole("button", { name: "Publish" }).click();
    const publicationDialog = page.getByRole("dialog", {
      name: "Publish candidate?",
    });
    await publicationDialog
      .getByLabel("Reason")
      .fill("Publish the independently approved immutable candidate");
    await publicationDialog.getByRole("button", { name: "Publish" }).click();
    await expect
      .poll(
        () =>
          recorded(
            state,
            "POST",
            `${supplyChainBase}/candidates/candidate-1/publish`,
          ).length,
      )
      .toBe(1);
    const publication = recorded(
      state,
      "POST",
      `${supplyChainBase}/candidates/candidate-1/publish`,
    )[0];
    expect(publication.body).toEqual({
      reason: "Publish the independently approved immutable candidate",
      receipt_digest: `sha256:${"e".repeat(64)}`,
    });
    expect(publication.headers?.["if-match"]).toBe("4");
    expect(publication.headers?.["idempotency-key"]).toMatch(/^wolf-ui-/);
    await expect(page.getByLabel("Status: Published")).toBeVisible();

    await page
      .getByRole("navigation", { name: "Scanner release management" })
      .getByRole("button", { name: "Releases" })
      .click();
    await page
      .getByRole("row", { name: /Open release scanner-set-2026\.31\.1/ })
      .click();
    await expect(
      page.getByRole("heading", {
        level: 1,
        name: "scanner-set-2026.31.1",
      }),
    ).toBeVisible();

    await page.getByRole("button", { name: "Promote" }).click();
    const promoteDialog = page.getByRole("dialog", {
      name: "Promote release?",
    });
    await promoteDialog
      .getByLabel("Reason")
      .fill("Canary evidence is complete and approved");
    await promoteDialog.getByRole("button", { name: "Promote" }).click();

    await expect(page).toHaveURL(/tab=rollouts.*rollout=rollout-1/);
    await expect(
      page.getByRole("heading", {
        level: 1,
        name: "Rollout to production",
      }),
    ).toBeVisible();
    const promote = recorded(
      state,
      "POST",
      `${supplyChainBase}/releases/release-next/promote`,
    );
    expect(promote).toHaveLength(1);
    expect(promote[0].body).toEqual({
      reason: "Canary evidence is complete and approved",
      target: "stable",
      strategy: "canary",
    });
  });

  test("administers policy validation, revision activation, dry runs, and restore", async ({
    page,
  }, testInfo) => {
    test.skip(
      !testInfo.project.name.startsWith("desktop-chromium-"),
      "Policy administration is covered once; responsive rendering is covered separately.",
    );
    const state = await installScannerApiMock(page);
    await page.goto("/scanners?tab=policy");

    await expect(
      page.getByRole("heading", { level: 1, name: "Release policy" }),
    ).toBeVisible();
    await expect(page.getByText(/Active revision 7/)).toBeVisible();
    await page.getByLabel("Required approvers").fill("2");

    await page.getByRole("button", { name: "Validate" }).click();
    await expect
      .poll(
        () =>
          recorded(state, "POST", `${supplyChainBase}/policy/validate`).length,
      )
      .toBe(1);
    await expect(
      page.locator("main").getByText("Policy is valid", { exact: true }),
    ).toBeVisible();
    expect(
      recorded(state, "POST", `${supplyChainBase}/policy/validate`)[0].body,
    ).toMatchObject({ rules: { required_approvals: 2 } });

    await page.getByRole("button", { name: "Save new revision" }).click();
    await expect
      .poll(() => recorded(state, "PUT", `${supplyChainBase}/policy`).length)
      .toBe(1);
    const savePolicy = recorded(state, "PUT", `${supplyChainBase}/policy`)[0];
    expect(savePolicy.body).toMatchObject({
      schedule: { timezone: "America/New_York" },
      rules: { required_approvals: 2 },
    });
    expect(savePolicy.headers?.["if-match"]).toBe("7");
    expect(savePolicy.headers?.["idempotency-key"]).toMatch(/^wolf-ui-/);
    await expect(page.getByText(/Active revision 8/)).toBeVisible();

    await page.getByRole("tab", { name: "Historical dry run" }).click();
    await page.getByLabel("Candidate").selectOption("candidate-1");
    await page.getByRole("button", { name: "Evaluate" }).click();
    await expect
      .poll(
        () =>
          recorded(state, "POST", `${supplyChainBase}/policy/dry-run`).length,
      )
      .toBe(1);
    expect(
      recorded(state, "POST", `${supplyChainBase}/policy/dry-run`)[0].body,
    ).toMatchObject({
      candidate_id: "candidate-1",
      rules: { required_approvals: 2 },
    });
    await expect(page.getByLabel("Status: Awaiting Approval")).toBeVisible();
    await expect(
      page.getByText("A second independent approval is required."),
    ).toBeVisible();

    await page.getByRole("tab", { name: "Revision history" }).click();
    const restoreButtons = page.getByRole("button", {
      name: "Restore as new revision",
    });
    await restoreButtons.last().click();
    const restoreDialog = page.getByRole("dialog", {
      name: "Restore policy revision 6?",
    });
    await restoreDialog
      .getByLabel("Reason")
      .fill("Restore the previously approved enterprise policy baseline");
    await restoreDialog
      .getByRole("button", { name: "Restore revision" })
      .click();
    await expect
      .poll(
        () =>
          recorded(
            state,
            "POST",
            `${supplyChainBase}/policy/revisions/6/restore`,
          ).length,
      )
      .toBe(1);
    const restorePolicy = recorded(
      state,
      "POST",
      `${supplyChainBase}/policy/revisions/6/restore`,
    )[0];
    expect(restorePolicy.body).toEqual({
      reason: "Restore the previously approved enterprise policy baseline",
    });
    expect(restorePolicy.headers?.["idempotency-key"]).toMatch(/^wolf-ui-/);
    await expect(page.getByText(/Active revision 9/)).toBeVisible();
    await expectSemanticPage(page);
    await expectAutomatedAccessibility(page);
  });

  test("administers opaque registry targets and verifies connectivity", async ({
    page,
  }, testInfo) => {
    test.skip(
      !testInfo.project.name.startsWith("desktop-chromium-"),
      "Registry administration is covered once; responsive operations are covered separately.",
    );
    const state = await installScannerApiMock(page);
    await page.goto("/scanners?tab=registries&registry=registry-mirror");

    await expect(
      page.getByRole("heading", { level: 1, name: "Registries and trust" }),
    ).toBeVisible();
    await expect(
      page.getByText("dckr_pat_registry_target_never_render"),
    ).toHaveCount(0);
    await page.getByRole("button", { name: "Add registry" }).click();
    await page
      .getByRole("textbox", { name: "Name", exact: true })
      .fill("Enterprise private registry");
    await page
      .getByRole("combobox", { name: "Role", exact: true })
      .selectOption("private");
    await page
      .getByRole("textbox", { name: "Registry host", exact: true })
      .fill("registry.example.com");
    await page
      .getByRole("textbox", { name: "Namespace", exact: true })
      .fill("security/scanners");
    await page
      .getByRole("textbox", { name: /^Credential secret reference/ })
      .fill("secret:11111111-1111-4111-8111-111111111111");
    await page
      .getByRole("textbox", { name: "Trust policy reference", exact: true })
      .fill("trust-policy://enterprise-release");
    await page
      .getByRole("textbox", { name: "Platforms", exact: true })
      .fill("linux/amd64, linux/arm64");
    await page.getByRole("button", { name: "Create registry" }).click();

    await expect
      .poll(
        () => recorded(state, "POST", `${supplyChainBase}/registries`).length,
      )
      .toBe(1);
    const createRegistry = recorded(
      state,
      "POST",
      `${supplyChainBase}/registries`,
    )[0];
    expect(createRegistry.body).toEqual({
      name: "Enterprise private registry",
      type: "private",
      host: "registry.example.com",
      namespace: "security/scanners",
      secret_reference: "secret:11111111-1111-4111-8111-111111111111",
      trust_policy_reference: "trust-policy://enterprise-release",
      platform_policy: { platforms: ["linux/amd64", "linux/arm64"] },
      enabled: true,
    });
    expect(createRegistry.headers?.["idempotency-key"]).toMatch(/^wolf-ui-/);
    await expect(page).toHaveURL(/registry=registry-created/);
    await expect(
      page.getByText("Configured (opaque Wolf secret)"),
    ).toBeVisible();

    await page.getByRole("button", { name: "Test connectivity" }).click();
    await expect
      .poll(
        () =>
          recorded(
            state,
            "POST",
            `${supplyChainBase}/registries/registry-created/check`,
          ).length,
      )
      .toBe(1);
    await expect(page.getByText("Registry reachable in 42 ms.")).toBeVisible();

    await page
      .getByRole("textbox", { name: "Name", exact: true })
      .fill("Enterprise registry – production");
    await page.getByRole("button", { name: "Save changes" }).click();
    await expect
      .poll(
        () =>
          recorded(
            state,
            "PATCH",
            `${supplyChainBase}/registries/registry-created`,
          ).length,
      )
      .toBe(1);
    const updateRegistry = recorded(
      state,
      "PATCH",
      `${supplyChainBase}/registries/registry-created`,
    )[0];
    expect(updateRegistry.body).toEqual({
      name: "Enterprise registry – production",
      type: "private",
      host: "registry.example.com",
      namespace: "security/scanners",
      trust_policy_reference: "trust-policy://enterprise-release",
      platform_policy: { platforms: ["linux/amd64", "linux/arm64"] },
      enabled: true,
    });
    expect(updateRegistry.headers?.["if-match"]).toBe("1");
    expect(updateRegistry.headers?.["idempotency-key"]).toMatch(/^wolf-ui-/);
    await expectSemanticPage(page);
    await expectAutomatedAccessibility(page);
  });

  test("supports keyboard-only navigation and a guarded rollback dialog", async ({
    page,
  }, testInfo) => {
    test.skip(
      !testInfo.project.name.startsWith("desktop-chromium-"),
      "Keyboard order is invariant across the responsive CSS layout.",
    );
    const state = await installScannerApiMock(page);
    await page.goto("/scanners?tab=rollouts&rollout=rollout-1");
    const rollback = page.getByRole("button", { name: "Roll back" });
    await expect(rollback).toBeVisible();

    await tabTo(page, rollback);
    await expectFocusIndicator(rollback);
    await page.keyboard.press("Enter");

    let dialog = page.getByRole("dialog", { name: "Rollback rollout?" });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByLabel("Reason")).toBeFocused();
    await expectDialogFocusTrap(page, dialog);
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(rollback).toBeFocused();

    await page.keyboard.press("Enter");
    dialog = page.getByRole("dialog", { name: "Rollback rollout?" });
    await expect(dialog.getByLabel("Reason")).toBeFocused();
    await dialog
      .getByLabel("Reason")
      .fill("Canary signature failure exceeds the approved threshold");
    await dialog
      .getByLabel("Type release-next to confirm")
      .fill("release-next");

    const confirm = dialog.getByRole("button", { name: "Rollback" });
    await tabTo(page, confirm);
    await expectFocusIndicator(confirm);
    await page.keyboard.press("Enter");

    await expect(dialog).toBeHidden();
    const rollbackRequests = recorded(
      state,
      "POST",
      `${supplyChainBase}/rollouts/rollout-1/rollback`,
    );
    expect(rollbackRequests).toHaveLength(1);
    expect(rollbackRequests[0].body).toEqual({
      reason: "Canary signature failure exceeds the approved threshold",
    });
  });

  test("inspects and safely retries a dead-lettered scanner notification", async ({
    page,
  }, testInfo) => {
    test.skip(
      !testInfo.project.name.startsWith("desktop-chromium-"),
      "The notification command contract is viewport-independent.",
    );
    const state = await installScannerApiMock(page);
    await page.goto("/scanners?tab=notifications");

    await expect(
      page.getByRole("heading", { level: 1, name: "Scanner notifications" }),
    ).toBeVisible();
    await page.getByRole("button", { name: "Dead letters" }).click();
    await page
      .getByRole("button", {
        name: "Open notification Stable Release Health Issue",
      })
      .click();

    await expect(page).toHaveURL(
      /tab=notifications.*notification=notification-dead/,
    );
    await expect(
      page.getByRole("heading", {
        level: 1,
        name: "Stable Release Health Issue",
      }),
    ).toBeVisible();
    await expect(page.getByLabel("Severity: Critical")).toBeVisible();
    await expect(page.getByLabel("Status: Dead Letter")).toBeVisible();
    await expect(
      page.getByText(/Destination Unavailable\. Review bounded evidence/i),
    ).toBeVisible();
    await expect(
      page.getByText("Endpoint returned 503 after bounded retries."),
    ).toHaveCount(0);
    await expect(
      page.getByRole("link", { name: /Open release/i }).first(),
    ).toHaveAttribute("href", "/scanners?tab=releases&release=release-next");
    await expect(page.getByText("dckr_pat_never_render")).toHaveCount(0);
    await expect(page.getByText("<script>")).toHaveCount(0);

    await expectSemanticPage(page);
    await expectAutomatedAccessibility(page);

    await page.getByRole("button", { name: "Retry delivery" }).click();
    const retryDialog = page.getByRole("dialog", {
      name: "Retry notification delivery?",
    });
    await retryDialog
      .getByLabel("Reason")
      .fill("Security operations webhook routing has been repaired");
    await retryDialog.getByRole("button", { name: "Retry delivery" }).click();

    await expect
      .poll(
        () =>
          recorded(
            state,
            "POST",
            `${supplyChainBase}/notifications/notification-dead/retry`,
          ).length,
      )
      .toBe(1);
    const request = recorded(
      state,
      "POST",
      `${supplyChainBase}/notifications/notification-dead/retry`,
    )[0];
    expect(request.body).toEqual({
      reason: "Security operations webhook routing has been repaired",
    });
    expect(request.headers?.["if-match"]).toBe('"notification-version-7"');
    expect(request.headers?.["idempotency-key"]).toMatch(/^wolf-ui-/);
    await expect(retryDialog).toBeHidden();
  });

  test("reviews active and resolved scanner alerts without exposing unsafe evidence", async ({
    page,
  }) => {
    const state = await installScannerApiMock(page);
    await page.goto("/scanners?tab=notifications");
    await page.getByRole("button", { name: "Operational alerts" }).click();

    await expect(page).toHaveURL(/tab=notifications.*notification_view=alerts/);
    await expect(
      page.getByRole("heading", { level: 1, name: "Scanner alerts" }),
    ).toBeVisible();
    await expect(page.getByLabel("Severity: Critical")).toBeVisible();
    await expect(page.getByLabel("Status: Open")).toBeVisible();
    await expect(
      page.getByText(
        "The latest production scanner rollout failed or rolled back.",
      ),
    ).toBeVisible();
    await expect(page.getByText("rollout-1")).toBeVisible();
    await expect(page.getByText("dckr_pat_alert_never_render")).toHaveCount(0);
    await expect(page.getByText("<script>")).toHaveCount(0);

    await page.getByRole("button", { name: "Inspect alert" }).click();
    await expect(page).toHaveURL(/alert=alert-rollout/);
    await expect(
      page.getByRole("heading", { level: 1, name: "Rollout Failure" }),
    ).toBeVisible();
    await expect(page.getByText("1 recorded cycle")).toBeVisible();
    await expect(
      page.getByText(/Exact transition timestamps are not exposed/i),
    ).toBeVisible();
    await expect(
      page.getByRole("link", { name: /Review rollout/i }),
    ).toHaveAttribute("href", "/scanners?tab=rollouts&rollout=rollout-1");
    await expect(
      page.getByRole("button", { name: /resolve|reopen/i }),
    ).toHaveCount(0);
    await expectSemanticPage(page);
    await expectAutomatedAccessibility(page);

    await page.getByRole("button", { name: "All alerts" }).click();
    await page.getByLabel("Lifecycle status").selectOption("resolved");
    await expect(page.getByLabel("Status: Resolved")).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "Missed Discovery" }),
    ).toBeVisible();
    await expect(page.getByText("No active scanner alerts")).toHaveCount(0);

    const pageWidths = await page.evaluate(() => ({
      body: document.body.scrollWidth,
      viewport: window.innerWidth,
    }));
    expect(pageWidths.body).toBeLessThanOrEqual(pageWidths.viewport);
    expect(
      state.requests.filter((request) =>
        request.pathname.startsWith(`${supplyChainBase}/alerts`),
      ),
    ).toEqual([]);
  });

  test("operates durable registry reconciliation and quarantine safely on desktop and mobile", async ({
    page,
  }) => {
    const state = await installScannerApiMock(page);
    await page.goto(
      "/scanners?tab=registries&registry_view=jobs&registry=registry-mirror&registry_job=registry-job-completed",
    );

    await expect(
      page.getByRole("heading", {
        level: 1,
        name: "Registry reconciliation",
      }),
    ).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "Exact per-image evidence" }),
    ).toBeVisible();
    const evidence = page.getByRole("region", {
      name: "Exact evidence for semgrep",
    });
    await expect(
      evidence.getByText("sha256:sbom-semgrep").first(),
    ).toBeVisible();
    await expect(evidence.getByText("Verified")).toHaveCount(4);
    await expect(
      page.getByRole("link", { name: "Audit operation" }),
    ).toHaveAttribute(
      "href",
      "/scanners?tab=audit&operation_id=op_registry_repair_0001",
    );
    for (const secret of [
      "dckr_pat_registry_summary_never_render",
      "dckr_pat_registry_error_never_render",
      "dckr_pat_registry_image_detail_never_render",
      "dckr_pat_registry_event_never_render",
    ]) {
      await expect(page.getByText(secret)).toHaveCount(0);
    }
    await expectSemanticPage(page);
    await expectAutomatedAccessibility(page);
    await expectNoPageOverflow(page);

    await page
      .getByRole("button", {
        name: "Open Reconcile job registry-job-dead",
      })
      .click();
    await expect(page.getByText("Registry Unavailable")).toBeVisible();
    await expect(
      page.getByText(
        /Confirm registry reachability and bounded egress policy/i,
      ),
    ).toBeVisible();
    await expect(
      page.getByText("dckr_pat_registry_error_never_render"),
    ).toHaveCount(0);
    await page.getByRole("button", { name: "Retry job" }).click();
    const retryDialog = page.getByRole("dialog", {
      name: "Retry dead-lettered registry job?",
    });
    await retryDialog
      .getByLabel("Reason")
      .fill("Registry routing and write authorization repaired");
    await retryDialog.getByRole("button", { name: "Retry job" }).click();
    await expect
      .poll(
        () =>
          recorded(
            state,
            "POST",
            `${supplyChainBase}/registry-jobs/registry-job-dead/retry`,
          ).length,
      )
      .toBe(1);
    const retryRequest = recorded(
      state,
      "POST",
      `${supplyChainBase}/registry-jobs/registry-job-dead/retry`,
    )[0];
    expect(retryRequest.body).toEqual({
      reason: "Registry routing and write authorization repaired",
    });
    expect(retryRequest.headers?.["if-match"]).toBe('"9"');
    expect(retryRequest.headers?.["idempotency-key"]).toMatch(/^wolf-ui-/);

    await page.getByLabel("Published release").selectOption("release-next");
    await page
      .getByLabel("Verified repair source")
      .selectOption("registry-primary");
    await page.getByRole("button", { name: "Repair drift" }).click();
    const repairDialog = page.getByRole("dialog", {
      name: "Repair registry drift?",
    });
    await expectFocusWithin(repairDialog);
    await repairDialog
      .getByLabel("Reason")
      .fill("Restore the exact verified release closure");
    const repairConfirm = repairDialog.getByRole("button", {
      name: "Queue repair",
    });
    await repairDialog
      .getByLabel("Type Docker Hub mirror to confirm")
      .fill("Docker Hub mirro");
    await expect(repairConfirm).toBeDisabled();
    await repairDialog
      .getByLabel("Type Docker Hub mirror to confirm")
      .fill("Docker Hub mirror");
    await expectAutomatedAccessibility(page, '[role="dialog"]');
    await repairConfirm.click();
    await expect
      .poll(
        () =>
          recorded(
            state,
            "POST",
            `${supplyChainBase}/registries/registry-mirror/jobs`,
          ).length,
      )
      .toBe(1);
    const repairRequest = recorded(
      state,
      "POST",
      `${supplyChainBase}/registries/registry-mirror/jobs`,
    )[0];
    expect(repairRequest.body).toEqual({
      kind: "repair",
      release_id: "release-next",
      source_registry_id: "registry-primary",
      re_sign_policy: "preserve",
      reason: "Restore the exact verified release closure",
      max_attempts: 5,
    });
    expect(repairRequest.headers?.["idempotency-key"]).toMatch(/^wolf-ui-/);

    await page
      .getByRole("navigation", { name: "Registry operations" })
      .getByRole("button", { name: "Quarantine inventory" })
      .click();
    await expect(page).toHaveURL(/registry_view=quarantine/);
    await expect(
      page.getByRole("heading", { level: 1, name: "Registry quarantine" }),
    ).toBeVisible();
    await expect(page.getByText("Potentially cleanup eligible")).toBeVisible();
    await expect(
      page.getByText("Cleanup blocked by visible state"),
    ).toBeVisible();
    await expect(
      page.getByText(/Database-reference authorization is still required/i),
    ).toBeVisible();
    await expect(
      page.getByText("dckr_pat_quarantine_metadata_never_render"),
    ).toHaveCount(0);
    await expectSemanticPage(page);
    await expectAutomatedAccessibility(page);
    await expectNoPageOverflow(page);

    await page.getByRole("button", { name: "Queue guarded cleanup" }).click();
    const cleanupDialog = page.getByRole("dialog", {
      name: "Queue guarded quarantine cleanup?",
    });
    await expectFocusWithin(cleanupDialog);
    await cleanupDialog
      .getByLabel("Reason")
      .fill("Expired unreferenced partial publication cleanup");
    await cleanupDialog
      .getByLabel("Type Docker Hub mirror to confirm")
      .fill("Docker Hub mirror");
    await expectAutomatedAccessibility(page, '[role="dialog"]');
    await cleanupDialog.getByRole("button", { name: "Queue cleanup" }).click();
    await expect
      .poll(
        () =>
          recorded(
            state,
            "POST",
            `${supplyChainBase}/registries/registry-mirror/cleanup-jobs`,
          ).length,
      )
      .toBe(1);
    const cleanupRequest = recorded(
      state,
      "POST",
      `${supplyChainBase}/registries/registry-mirror/cleanup-jobs`,
    )[0];
    expect(cleanupRequest.body).toEqual({
      reason: "Expired unreferenced partial publication cleanup",
      max_attempts: 5,
    });
    expect(cleanupRequest.headers?.["idempotency-key"]).toMatch(/^wolf-ui-/);
  });

  test("renders degraded operations with actionable semantics @visual", async ({
    page,
  }) => {
    await installScannerApiMock(page, { mode: "read_only" });
    await page.goto("/scanners?tab=operations");

    await expect(
      page.getByRole("heading", { level: 1, name: "Release Operations" }),
    ).toBeVisible();
    await expect(
      page.getByRole("table", {
        name: "Release-factory component readiness",
      }),
    ).toBeVisible();
    await expect(page.getByText("Expired Lease")).toBeVisible();
    const reliability = page.getByRole("region", {
      name: "Build reliability and durable queues",
    });
    await expect(reliability.getByText("Completed Builds")).toBeVisible();
    await expect(
      reliability.getByText(/4 queued, retrying, or in delivery/i),
    ).toBeVisible();
    await expect(reliability.getByText(/customer_repository_id/i)).toHaveCount(
      0,
    );
    await expect(
      reliability.getByRole("link", { name: "Review Candidates" }).first(),
    ).toHaveAttribute("href", "/scanners?tab=candidates");
    await expect(
      page.getByRole("link", { name: "Review Registries" }),
    ).toBeVisible();
    await expect(page.getByRole("alert").first()).toBeVisible();

    await expectSemanticPage(page);
    await expectAutomatedAccessibility(page);
    await settleVisuals(page);
    await expect(page.locator("main")).toHaveScreenshot(
      "scanner-operations-degraded.png",
    );
  });

  test("deep-filters, copies, and exports bounded audit correlation", async ({
    page,
  }) => {
    const copiedKey = "wolf-e2e-copied-correlation";
    await page.addInitScript((storageKey) => {
      Object.defineProperty(navigator, "clipboard", {
        configurable: true,
        value: {
          writeText: async (value: string) => {
            window.sessionStorage.setItem(storageKey, value);
          },
        },
      });
    }, copiedKey);
    const state = await installScannerApiMock(page);
    const operationId = "op_build_release_0001";
    const traceId = "0123456789abcdef0123456789abcdef";
    await page.goto(
      `/scanners?tab=audit&operation_id=${encodeURIComponent(operationId)}`,
    );

    await expect(
      page.getByRole("heading", { level: 1, name: "Scanner release audit" }),
    ).toBeVisible();
    await expect(page.getByText("Scanner Build Completed")).toBeVisible();
    await expect(page.getByText("Scanner Candidate Created")).toHaveCount(0);
    await expect(page.getByText("dckr_pat_audit_never_render")).toHaveCount(0);
    await page.getByRole("button", { name: "Copy Operation ID" }).click();
    await expect(
      page.getByText("Operation ID copied to the clipboard.", {
        exact: true,
      }),
    ).toBeAttached();
    await expect
      .poll(() =>
        page.evaluate(
          (storageKey) => sessionStorage.getItem(storageKey),
          copiedKey,
        ),
      )
      .toBe(operationId);

    const exportLink = page.getByRole("link", { name: "Export JSONL" });
    await expect(exportLink).toHaveAttribute(
      "href",
      `/api/v1/scanner-supply-chain/audit/export?format=jsonl&operation_id=${operationId}`,
    );

    const correlationType = page.getByLabel("Correlation type");
    const correlation = page.getByLabel("Exact correlation identifier");
    await correlationType.selectOption("trace");
    const requestsBeforeMalformed = state.getRequests.filter((request) =>
      request.startsWith(`${supplyChainBase}/audit?`),
    ).length;
    await correlation.fill("not-a-valid-trace");
    await expect(
      page.getByText(/Trace IDs must be 32 lowercase hexadecimal/i),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Apply exact filter" }),
    ).toBeDisabled();
    expect(
      state.getRequests.filter((request) =>
        request.startsWith(`${supplyChainBase}/audit?`),
      ),
    ).toHaveLength(requestsBeforeMalformed);

    await correlation.fill(traceId);
    await page.getByRole("button", { name: "Apply exact filter" }).click();
    await expect(page).toHaveURL(new RegExp(`tab=audit.*trace_id=${traceId}`));
    await expect(page).not.toHaveURL(/operation_id=/);
    await expect(page.getByText("Scanner Candidate Created")).toBeVisible();
    await expect(exportLink).toHaveAttribute(
      "href",
      `/api/v1/scanner-supply-chain/audit/export?format=jsonl&trace_id=${traceId}`,
    );

    await page.getByLabel("Actor").fill("scanner-build-worker");
    await expect(exportLink).toHaveAttribute(
      "href",
      `/api/v1/scanner-supply-chain/audit/export?format=jsonl&actor=scanner-build-worker&trace_id=${traceId}`,
    );
    await expectSemanticPage(page);
    await expectAutomatedAccessibility(page);
    await expectNoPageOverflow(page);

    await correlation.fill("");
    await page.getByRole("button", { name: "Clear filter" }).click();
    await expect(page).not.toHaveURL(/trace_id=|operation_id=/);
  });

  test("renders integrity metadata and both artifact diffs @visual", async ({
    page,
  }) => {
    await installScannerApiMock(page);
    await page.goto("/scanners?tab=candidates&candidate=candidate-1");
    await page.getByRole("tab", { name: "Changes" }).click();

    const manifest = page.getByRole("region", {
      name: "Manifest Diff content",
    });
    const lock = page.getByRole("region", {
      name: "Release Lock Diff content",
    });
    await expect(manifest).toContainText("semgrep: 1.128.0");
    await expect(lock).toContainText("checksum: sha256:tool0001");
    await expect(page.getByText(/truncated/i)).toBeVisible();
    await expect(manifest).toHaveAttribute("tabindex", "0");
    await expect(lock).toHaveAttribute("tabindex", "0");

    await expectSemanticPage(page);
    await expectAutomatedAccessibility(page);
    await settleVisuals(page);
    await expect(page.locator("main")).toHaveScreenshot(
      "scanner-candidate-artifact-diffs.png",
    );
  });
});

for (const mode of [
  "read_only",
  "candidate",
  "canary",
  "stable_control",
] as const) {
  test(`enforces the ${mode} capability stage`, async ({ page }, testInfo) => {
    test.skip(
      !testInfo.project.name.startsWith("desktop-chromium-"),
      "Capability authorization is viewport-independent.",
    );
    await installScannerApiMock(page, { mode });

    if (mode === "read_only" || mode === "candidate") {
      await page.goto("/scanners?tab=updates");
      const check = page.getByRole("button", { name: "Check all now" });
      const select = page.getByRole("checkbox", { name: "Select semgrep" });
      if (mode === "read_only") {
        await expect(check).toBeDisabled();
        await expect(select).toBeDisabled();
      } else {
        await expect(check).toBeEnabled();
        await expect(select).toBeEnabled();
      }
      return;
    }

    await page.goto("/scanners?tab=releases&release=release-next");
    await expect(page.getByRole("button", { name: "Promote" })).toBeEnabled();
    const revoke = page.getByRole("button", { name: "Revoke" });
    if (mode === "canary") {
      await expect(revoke).toBeDisabled();
    } else {
      await expect(revoke).toBeEnabled();
    }
  });
}

async function expectSemanticPage(page: Page) {
  await expect(page.locator("main h1")).toHaveCount(1);
  const unnamedInteractive = await page.locator("main").evaluate((main) => {
    const controls = main.querySelectorAll(
      'button, a[href], input:not([type="hidden"]), select, textarea, [tabindex="0"]',
    );
    return [...controls]
      .filter((control) => {
        const element = control as HTMLElement;
        if (
          element.getAttribute("aria-hidden") === "true" ||
          element.closest('[aria-hidden="true"]')
        ) {
          return false;
        }
        const labelledBy = element.getAttribute("aria-labelledby");
        const labelText = labelledBy
          ?.split(/\s+/)
          .map((id) => document.getElementById(id)?.textContent ?? "")
          .join(" ");
        const explicitLabel =
          element.id &&
          document.querySelector(`label[for="${CSS.escape(element.id)}"]`)
            ?.textContent;
        const wrappingLabel = element.closest("label")?.textContent;
        return ![
          element.getAttribute("aria-label"),
          element.getAttribute("title"),
          labelText,
          explicitLabel,
          wrappingLabel,
          element.textContent,
        ].some((value) => value?.trim());
      })
      .map((control) => control.outerHTML.slice(0, 200));
  });
  expect(
    unnamedInteractive,
    `Every interactive scanner-management control needs an accessible name:\n${unnamedInteractive.join("\n")}`,
  ).toEqual([]);
}

async function expectAutomatedAccessibility(page: Page, include = "main") {
  const results = await new AxeBuilder({ page })
    .include(include)
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    .analyze();
  const report = results.violations
    .map(
      (violation) =>
        `${violation.id}: ${violation.help}\n${violation.nodes
          .map((node) => `  ${node.target.join(" ")} — ${node.failureSummary}`)
          .join("\n")}`,
    )
    .join("\n\n");
  expect(results.violations, report).toEqual([]);
}

async function tabTo(page: Page, target: Locator, limit = 100) {
  for (let attempt = 0; attempt < limit; attempt += 1) {
    await page.keyboard.press("Tab");
    if (
      await target.evaluate(
        (element) => element === element.ownerDocument.activeElement,
      )
    ) {
      return;
    }
  }
  throw new Error(`Keyboard focus did not reach ${await target.innerText()}`);
}

async function expectFocusIndicator(target: Locator) {
  const visible = await target.evaluate((element) => {
    const style = getComputedStyle(element);
    const hasOutline =
      style.outlineStyle !== "none" &&
      Number.parseFloat(style.outlineWidth) > 0;
    const hasShadow =
      style.boxShadow !== "none" && style.boxShadow.trim().length > 0;
    return hasOutline || hasShadow;
  });
  expect(
    visible,
    "Focused control should expose an outline or focus ring",
  ).toBe(true);
}

async function expectDialogFocusTrap(page: Page, dialog: Locator) {
  const focusables = dialog.locator(
    'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
  );
  const count = await focusables.count();
  expect(
    count,
    "Dialog should contain keyboard-operable controls",
  ).toBeGreaterThan(1);

  await focusables.nth(count - 1).focus();
  await page.keyboard.press("Tab");
  await expectFocusWithin(dialog);
  await focusables.first().focus();
  await page.keyboard.press("Shift+Tab");
  await expectFocusWithin(dialog);
}

async function expectFocusWithin(container: Locator) {
  const containsFocus = await container.evaluate((element) =>
    element.contains(element.ownerDocument.activeElement),
  );
  expect(
    containsFocus,
    "Keyboard focus must remain within the open dialog",
  ).toBe(true);
}

async function expectNoPageOverflow(page: Page) {
  const widths = await page.evaluate(() => ({
    body: document.body.scrollWidth,
    document: document.documentElement.scrollWidth,
    viewport: window.innerWidth,
    mainScroll:
      document.querySelector<HTMLElement>("#main-content")?.scrollWidth,
    mainClient:
      document.querySelector<HTMLElement>("#main-content")?.clientWidth,
  }));
  expect(
    Math.max(widths.body, widths.document),
    `Scanner page should fit the ${widths.viewport}px viewport`,
  ).toBeLessThanOrEqual(widths.viewport);
  if (widths.mainScroll !== undefined && widths.mainClient !== undefined) {
    expect(
      widths.mainScroll,
      "Main content should contain horizontal overflow inside its designated scroll regions",
    ).toBeLessThanOrEqual(widths.mainClient);
  }
}

async function expectActiveControlWithinScrollableNavigation(
  navigation: Locator,
) {
  const contained = await navigation.evaluate((element) => {
    const active = element.querySelector<HTMLElement>('[aria-current="page"]');
    if (!active) return false;
    const navigationBounds = element.getBoundingClientRect();
    const activeBounds = active.getBoundingClientRect();
    return (
      activeBounds.left >= navigationBounds.left - 1 &&
      activeBounds.right <= navigationBounds.right + 1
    );
  });
  expect(
    contained,
    "The active navigation control should be scrolled into the visible tab strip",
  ).toBe(true);
}

async function settleVisuals(page: Page) {
  await page.evaluate(async () => {
    await document.fonts.ready;
  });
  await page.waitForTimeout(100);
}
