package corebanking

class CoreBanking {
    fun postToLedger(paymentId: String, amountCents: Long) {
        println("legacy ledger call for $paymentId: $amountCents")
    }
}
