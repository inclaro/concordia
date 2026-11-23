export interface ProfileRecord {
  customerId: string;
  fullName: string;
  nationalId: string;
}

export class ProfileStore {
  load(customerId: string): ProfileRecord {
    return { customerId, fullName: "Customer " + customerId, nationalId: "redacted" };
  }
}
