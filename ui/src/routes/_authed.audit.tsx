// The audit log moved into Settings → Audit (admin). Keep /audit working as a
// redirect so old links and bookmarks still land in the right place.
import { createFileRoute, redirect } from "@tanstack/react-router";

export const Route = createFileRoute("/_authed/audit")({
  beforeLoad: () => {
    throw redirect({ to: "/settings", search: { tab: "audit" } });
  },
});
