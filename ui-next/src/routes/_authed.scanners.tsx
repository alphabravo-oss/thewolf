// /scanners → /settings?tab=scanners.
//
// Scanner-backend admin moved into Settings since it's a single place
// for "things you configure once and walk away from" (scanner config,
// pulls, doctor checks). Keep the route file as a redirect so existing
// bookmarks and deep links land in the right tab instead of 404ing.
import { createFileRoute, Navigate } from "@tanstack/react-router";

export const Route = createFileRoute("/_authed/scanners")({
  component: ScannersRedirect,
});

function ScannersRedirect() {
  return <Navigate to="/settings" search={{ tab: "scanners" }} replace />;
}
