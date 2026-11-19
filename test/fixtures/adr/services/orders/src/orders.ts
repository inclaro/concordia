export function placeOrder(orderId: string, items: number[]): string {
  return orderId + ":" + items.length;
}
