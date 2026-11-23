import { sendAlert } from "../../notifications-service/src/api/index";

export function raiseFraudAlert(paymentId: string, score: number): boolean {
  return sendAlert("fraud-team", `payment ${paymentId} scored ${score}`);
}
