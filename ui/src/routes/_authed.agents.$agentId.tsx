import { createFileRoute } from "@tanstack/react-router";
import { FixJobView } from "@/components/fixes/fix-job-view";

export const Route = createFileRoute("/_authed/agents/$agentId")({
  component: AgentDetailPage,
});

function AgentDetailPage() {
  const { agentId } = Route.useParams();
  return <FixJobView fixId={agentId} backTo="/agents" />;
}
