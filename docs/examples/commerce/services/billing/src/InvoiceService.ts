import { ShipmentStore } from "../../fulfillment/src/internal/ShipmentStore";
import { ShipmentRequest } from "../../../packages/fulfillment-contract/src/index";

export class InvoiceService {
  issue(orderId: string, amountCents: number): ShipmentRequest {
    const store = new ShipmentStore();
    store.reserve(orderId);
    return { orderId, total: amountCents };
  }
}
