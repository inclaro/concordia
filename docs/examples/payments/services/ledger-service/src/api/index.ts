import { LedgerTable } from "../db/schema";

export function postEntry(paymentId: string, amountCents: number): number {
  const table = new LedgerTable();
  return table.insert(paymentId, amountCents);
}
