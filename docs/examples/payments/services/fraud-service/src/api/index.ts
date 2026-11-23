export interface RiskDecision {
  paymentId: string;
  blocked: boolean;
}

export function assessPayment(paymentId: string, amountCents: number): RiskDecision {
  return { paymentId, blocked: amountCents > 1_000_000 };
}
