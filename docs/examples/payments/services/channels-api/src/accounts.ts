import { ProfileStore } from "../../customer-profile/src/internal/ProfileStore";

export interface AccountSummary {
  customerId: string;
  displayName: string;
}

export function accountSummary(customerId: string): AccountSummary {
  const store = new ProfileStore();
  const profile = store.load(customerId);
  return { customerId, displayName: profile.fullName };
}
