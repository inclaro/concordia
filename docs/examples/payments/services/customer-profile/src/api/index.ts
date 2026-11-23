export interface PublicProfile {
  customerId: string;
  displayName: string;
}

export function publicProfile(customerId: string): PublicProfile {
  return { customerId, displayName: "Customer " + customerId };
}
