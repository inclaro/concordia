export class RiskModel {
  private readonly threshold = 500_000;

  score(paymentId: string, amountCents: number): number {
    if (paymentId.length === 0) return 1;
    return Math.min(1, amountCents / this.threshold);
  }
}
