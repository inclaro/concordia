export interface PaymentRequest {
  paymentId: string;
  amountCents: number;
}

export interface PaymentResult {
  paymentId: string;
  status: "accepted" | "pending";
}

export function authorizePayment(request: PaymentRequest): PaymentResult {
  return { paymentId: request.paymentId, status: "pending" };
}

export function paymentStatus(paymentId: string): PaymentResult {
  return { paymentId, status: "accepted" };
}
