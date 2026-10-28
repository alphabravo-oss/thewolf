import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { AddButton } from "@/components/ui/add-button";
import { FormDialog } from "@/components/ui/form-dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { StatusBadge } from "@/components/ui/status-badge";
import { safeErrorMessage } from "@/lib/safe-display";
import { Field, RowActions, SettingsCard, SettingsTable, shortDate, TypedConfirmDialog } from "./shared";

interface UserSummary {
  id: string;
  email: string;
  role: string;
  created_at: string;
  updated_at: string;
}

type NewUser = { email: string; password: string; role: string };
const blank: NewUser = { email: "", password: "", role: "user" };

export function UsersSection() {
  const qc = useQueryClient();
  const meQ = useQuery({
    queryKey: ["me"],
    queryFn: async () => (await api.get<{ id: string }>("/auth/me")).data,
  });
  const q = useQuery({
    queryKey: ["users"],
    queryFn: async () => (await api.get<UserSummary[]>("/users")).data ?? [],
  });
  const [adding, setAdding] = useState(false);
  const [form, setForm] = useState<NewUser>(blank);
  const [touched, setTouched] = useState(false);
  const [deleting, setDeleting] = useState<UserSummary | null>(null);
  const [roleChange, setRoleChange] = useState<{ user: UserSummary; role: string } | null>(null);

  const closeAdd = (open: boolean) => {
    setAdding(open);
    if (!open) {
      setForm(blank);
      setTouched(false);
      create.reset();
    }
  };
  const create = useMutation({
    mutationFn: (body: NewUser) => api.post("/users", { ...body, email: body.email.trim().toLowerCase() }),
    onSuccess: () => {
      toast.success("User created");
      closeAdd(false);
      qc.invalidateQueries({ queryKey: ["users"] });
    },
  });
  const setRole = useMutation({
    mutationFn: ({ id, role }: { id: string; role: string }) => api.put(`/users/${id}/role`, { role }),
    onSuccess: () => {
      toast.success("Role updated");
      setRoleChange(null);
      return qc.invalidateQueries({ queryKey: ["users"] });
    },
    onError: (e) => toast.error(safeErrorMessage(e, "Role change failed")),
  });
  const del = useMutation({
    mutationFn: (id: string) => api.delete(`/users/${id}`),
    onSuccess: () => {
      toast.success("User deleted");
      qc.invalidateQueries({ queryKey: ["users"] });
      setDeleting(null);
    },
    onError: (e) => toast.error(safeErrorMessage(e, "Delete failed")),
  });

  const meId = meQ.data?.id;
  const adminCount = (q.data ?? []).filter((u) => u.role === "admin").length;
  const emailInvalid = touched && !/^\S+@\S+$/.test(form.email.trim());
  const pwInvalid = touched && form.password.length < 12;

  return (
    <SettingsCard
      title="Users"
      description="Everyone who can sign in. Roles decide who can change settings."
      actions={<AddButton thing="user" onClick={() => setAdding(true)} />}
      flush
    >
      <SettingsTable
        query={q}
        keyExtractor={(u) => u.id}
        persistKey="wolf-settings-users"
        searchPlaceholder="Search users..."
        empty={{ title: "No users", description: "Create the first account for your team.", actionLabel: "Add user", onAction: () => setAdding(true) }}
        columns={[
          {
            key: "email",
            header: "Email",
            sortAccessor: (u) => u.email,
            accessor: (u) => (
              <span>
                {u.email}
                {u.id === meId ? <span className="ml-2 text-[10px] uppercase tracking-wide text-muted-foreground">you</span> : null}
              </span>
            ),
          },
          {
            key: "role",
            header: "Role",
            sortAccessor: (u) => u.role,
            accessor: (u) => <StatusBadge status={u.role === "admin" ? "info" : "neutral"} label={u.role === "admin" ? "Admin" : "User"} size="sm" showDot={false} />,
          },
          { key: "created", header: "Created", sortAccessor: (u) => u.created_at, accessor: (u) => <span className="text-xs text-muted-foreground">{shortDate(u.created_at)}</span> },
          {
            key: "actions",
            header: "",
            sortable: false,
            accessor: (u) => {
              const isAdmin = u.role === "admin";
              const lockDemote = isAdmin && adminCount <= 1;
              return (
                <RowActions
                  label={u.email}
                  actions={[
                    u.id !== meId && !lockDemote && { label: isAdmin ? "Make user" : "Make admin", onSelect: () => setRoleChange({ user: u, role: isAdmin ? "user" : "admin" }) },
                    u.id !== meId && { label: "Delete user", destructive: true, onSelect: () => setDeleting(u) },
                  ]}
                />
              );
            },
          },
        ]}
      />

      <FormDialog
        open={adding}
        onOpenChange={closeAdd}
        title="Add user"
        description="They can change their password after signing in."
        submitLabel="Create user"
        pending={create.isPending}
        dirty={!!form.email || !!form.password}
        error={create.isError ? safeErrorMessage(create.error, "Create failed") : undefined}
        onSubmit={() => {
          setTouched(true);
          if (/^\S+@\S+$/.test(form.email.trim()) && form.password.length >= 12) create.mutate(form);
        }}
      >
        <Field label="Email" error={emailInvalid ? "Enter a valid email address." : undefined}>
          <Input type="email" value={form.email} placeholder="user@example.com" autoComplete="off"
            onChange={(e) => setForm({ ...form, email: e.target.value })} aria-invalid={emailInvalid} />
        </Field>
        <Field label="Password" error={pwInvalid ? "Use at least 12 characters." : undefined}>
          <Input type="password" value={form.password} placeholder="At least 12 characters" autoComplete="new-password"
            onChange={(e) => setForm({ ...form, password: e.target.value })} aria-invalid={pwInvalid} />
        </Field>
        <Field label="Role">
          <Select value={form.role} onValueChange={(role) => setForm({ ...form, role })}>
            <SelectTrigger aria-label="Role"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="user">User</SelectItem>
              <SelectItem value="admin">Admin</SelectItem>
            </SelectContent>
          </Select>
        </Field>
      </FormDialog>

      <ConfirmDialog
        open={!!roleChange}
        onOpenChange={(o) => !o && setRoleChange(null)}
        variant="default"
        title={roleChange?.role === "admin" ? `Make ${roleChange.user.email} an admin?` : `Remove admin from ${roleChange?.user.email ?? ""}?`}
        description={roleChange?.role === "admin" ? "Admins can change every setting, user and key." : "They lose access to Settings immediately."}
        confirmLabel={roleChange?.role === "admin" ? "Make admin" : "Make user"}
        pending={setRole.isPending}
        onConfirm={() => roleChange && setRole.mutate({ id: roleChange.user.id, role: roleChange.role })}
      />
      <TypedConfirmDialog
        open={!!deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        expected={deleting?.email ?? ""}
        title={`Delete user “${deleting?.email ?? ""}”?`}
        description="Their API keys stop working and their sessions end. This cannot be undone."
        confirmLabel="Delete user"
        pending={del.isPending}
        onConfirm={() => deleting && del.mutate(deleting.id)}
      />
    </SettingsCard>
  );
}
