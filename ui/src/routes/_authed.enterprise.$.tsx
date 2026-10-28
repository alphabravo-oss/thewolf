// The Enterprise section is folded into Settings; old /enterprise links land on
// the matching Settings section.
import { createFileRoute, redirect } from "@tanstack/react-router";

const TARGETS: Record<string, string> = {
  identity: "sso",
  "group-mappings": "sso",
  siem: "integrations",
  ticketing: "integrations",
  integrations: "integrations",
};

export const Route = createFileRoute("/_authed/enterprise/$")({
  beforeLoad: ({ params }) => {
    throw redirect({ to: "/settings", search: { tab: TARGETS[params._splat?.split("/")[0] ?? ""] ?? "license" } });
  },
});
