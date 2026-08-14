import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Locator, type Page } from "@playwright/test";
import { installScannerApiMock, recorded } from "./scanner-api-mock";

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
    ).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: "Set up scanners (pull images)" }),
    ).toBeEnabled();
    await expect(
      page.getByRole("button", { name: "Rebuild (local)" }),
    ).toHaveCount(0);
    await expect(
      page.getByRole("heading", { name: "Configured scanner images" }),
    ).toBeVisible();

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
    await expect(
      page.getByRole("button", { name: "Open supply-chain console" }),
    ).toHaveCount(0);
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
    await page.goto("/collections");

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
    await page.goto("/collections");

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
    await page.goto("/collections");

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
    `Every interactive control needs an accessible name:\n${unnamedInteractive.join("\n")}`,
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
    `Page should fit the ${widths.viewport}px viewport`,
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
