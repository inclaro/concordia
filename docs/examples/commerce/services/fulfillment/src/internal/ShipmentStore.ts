export class ShipmentStore {
  private reserved = new Set<string>();

  reserve(orderId: string): boolean {
    if (this.reserved.has(orderId)) return false;
    this.reserved.add(orderId);
    return true;
  }
}
