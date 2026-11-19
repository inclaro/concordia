import { charge, Charge } from "../../billing/src/billing";

function price(items: number[]): number {
  return items.reduce((sum, item) => sum + item, 0);
}

function validate(items: number[]): boolean {
  return items.length > 0 && price(items) > 0;
}

export function placeOrder(orderId: string, items: number[]): Charge | null {
  if (!validate(items)) return null;
  return charge(orderId, price(items));
}
