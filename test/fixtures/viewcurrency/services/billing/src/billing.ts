export interface Charge {
  orderId: string;
  cents: number;
}

export function charge(orderId: string, cents: number): Charge {
  return { orderId, cents };
}
