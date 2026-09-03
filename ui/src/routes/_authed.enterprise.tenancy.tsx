import { createFileRoute } from "@tanstack/react-router";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { EnterpriseChrome, RecordsPanel } from "@/components/enterprise-records";

export const Route = createFileRoute("/_authed/enterprise/tenancy")({
  component: TenancyPage,
});

function TenancyPage() {
  return (
    <EnterpriseChrome
      title="Tenancy"
      description="Workspaces, applications, projects, and custom roles for this Enterprise install."
    >
      <Tabs defaultValue="workspaces">
        <TabsList>
          <TabsTrigger value="workspaces">Workspaces</TabsTrigger>
          <TabsTrigger value="applications">Applications</TabsTrigger>
          <TabsTrigger value="projects">Projects</TabsTrigger>
          <TabsTrigger value="roles">Custom roles</TabsTrigger>
        </TabsList>
        <TabsContent value="workspaces">
          <RecordsPanel path="/enterprise/workspaces" title="Workspaces" />
        </TabsContent>
        <TabsContent value="applications">
          <RecordsPanel path="/enterprise/applications" title="Applications" />
        </TabsContent>
        <TabsContent value="projects">
          <RecordsPanel path="/enterprise/projects" title="Projects" />
        </TabsContent>
        <TabsContent value="roles">
          <RecordsPanel path="/enterprise/custom-roles" title="Custom roles" />
        </TabsContent>
      </Tabs>
    </EnterpriseChrome>
  );
}
