// Layout shell for /collections + /collections/$collectionId.
//
// TanStack Router automatically nests `_authed.collections.$collectionId.tsx`
// underneath `_authed.collections.tsx`. The parent therefore MUST render an
// <Outlet /> for any child route to display — otherwise clicking a
// collection card changes the URL but the page still shows the parent's
// content. The list lives in `_authed.collections.index.tsx`; this file
// is just the pass-through layout.
import { createFileRoute, Outlet } from "@tanstack/react-router";

export const Route = createFileRoute("/_authed/collections")({
  component: CollectionsLayout,
});

function CollectionsLayout() {
  return <Outlet />;
}
