// Redirect /scans/$scanId/live → /scans/$scanId.
//
// The dedicated live page was redundant once the detail page got a
// per-tool status panel + cancel buttons + partial-results banner
// (all of which poll every 3s while the scan is running). Keep the
// route file as a soft-redirect rather than deleting it so external
// links and bookmarks land somewhere sensible instead of 404ing.
import { createFileRoute, Navigate } from "@tanstack/react-router";

export const Route = createFileRoute("/_authed/scans/$scanId/live")({
  component: LiveRedirect,
});

function LiveRedirect() {
  const { scanId } = Route.useParams();
  return <Navigate to="/scans/$scanId" params={{ scanId }} replace />;
}
