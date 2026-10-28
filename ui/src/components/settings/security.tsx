import { KnobsCard, type Knob } from "./knobs";

const SIGN_IN: Knob[] = [
  {
    key: "registration_enabled",
    label: "Self-service registration",
    help: "When off, new accounts can only be created from the Users section. The first account can always bootstrap the system.",
    type: "bool",
  },
  {
    key: "mfa_required",
    label: "Require two-factor auth",
    help: "When on, every user must enroll an authenticator app before they can use Wolf. Existing sessions are confined to Account & MFA until they enroll, and MFA cannot be self-disabled.",
    type: "bool",
  },
];

export function SecuritySection() {
  return <KnobsCard title="Sign-in policy" description="Who can create accounts and how they authenticate." knobs={SIGN_IN} />;
}
