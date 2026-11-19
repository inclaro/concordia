import { InvoiceService } from "../../billing/src/InvoiceService";

export function notifyShipped(orderId: string): string {
  const invoices = new InvoiceService();
  return JSON.stringify(invoices.issue(orderId, 0));
}
