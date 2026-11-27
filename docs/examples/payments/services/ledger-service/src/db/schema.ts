export interface LedgerRow {
  paymentId: string;
  amountCents: number;
}

export class LedgerTable {
  private rows: LedgerRow[] = [];

  insert(paymentId: string, amountCents: number): number {
    this.rows.push({ paymentId, amountCents });
    return this.rows.length;
  }

  totals(): number {
    return this.rows.reduce((sum, row) => sum + row.amountCents, 0);
  }
}
