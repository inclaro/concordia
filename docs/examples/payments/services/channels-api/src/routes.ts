import { authorizePayment, PaymentRequest } from "../../payments-service/src/api/index";
import { accountSummary } from "./accounts";

export function handlePayment(request: PaymentRequest): string {
  const result = authorizePayment(request);
  return JSON.stringify(result);
}

export function handleAccount(customerId: string): string {
  return JSON.stringify(accountSummary(customerId));
}
