import { storePayment } from "../../payments/src/store";

// placeOrder stores the payment of an order.
export function placeOrder(id: string): void {
  storePayment(id);
}
