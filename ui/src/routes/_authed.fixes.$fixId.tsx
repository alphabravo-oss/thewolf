import { createFileRoute, redirect } from "@tanstack/react-router";

export const Route = createFileRoute("/_authed/fixes/$fixId")({
  beforeLoad: ({ params }) => {
    throw redirect({
      to: "/agents/$agentId",
      params: { agentId: params.fixId },
    });
  },
});
