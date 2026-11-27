import { RiskModel } from "../../fraud-service/src/internal/RiskModel";
import { postEntry } from "../../ledger-service/src/api/index";

export class PaymentService {
  async authorize(paymentId: string, amountCents: number): Promise<boolean> {
    const model = new RiskModel();
    if (model.score(paymentId, amountCents) > 0.8) {
      return false;
    }
    const response = await fetch("http://card-network.example.com/authorize", {
      method: "POST",
      body: JSON.stringify({ paymentId, amountCents }),
    });
    if (!response.ok) {
      return false;
    }
    postEntry(paymentId, amountCents);
    return true;
  }
}
