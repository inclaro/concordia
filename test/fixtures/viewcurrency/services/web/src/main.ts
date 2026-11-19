import { placeOrder } from "../../orders/src/orders";

export function checkout(orderId: string): string {
  const receipt = placeOrder(orderId, [1200, 350]);
  return receipt ? "paid" : "empty";
}
