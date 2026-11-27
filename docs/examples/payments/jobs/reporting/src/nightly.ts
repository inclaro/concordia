import { LedgerTable } from "../../../services/ledger-service/src/db/schema";

export function nightlyTotals(): number {
  const table = new LedgerTable();
  return table.totals();
}
