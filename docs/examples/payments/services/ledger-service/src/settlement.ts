import { paymentStatus } from "../../payments-service/src/api/index";

export function settle(paymentIds: string[]): string[] {
  return paymentIds.filter((id) => paymentStatus(id).status === "accepted");
}
