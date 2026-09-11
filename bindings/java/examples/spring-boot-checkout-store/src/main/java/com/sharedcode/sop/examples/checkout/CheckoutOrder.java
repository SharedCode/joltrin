package com.sharedcode.sop.examples.checkout;

import java.io.Serializable;

/** POJO stored in the "checkout_orders" B-Tree. SOP stores plain objects directly, no ORM mapping layer needed. */
public class CheckoutOrder implements Serializable {

    public String id;
    public String customerId;
    public int itemCount;
    public long totalCents;
    public String status; // PENDING, PAID, CANCELLED

    public CheckoutOrder() {
        // required for Jackson deserialization, same requirement the sop4j tutorial's Product class notes
    }

    public CheckoutOrder(String id, String customerId, int itemCount, long totalCents, String status) {
        this.id = id;
        this.customerId = customerId;
        this.itemCount = itemCount;
        this.totalCents = totalCents;
        this.status = status;
    }

    @Override
    public String toString() {
        return String.format(
                "CheckoutOrder[id=%s, customerId=%s, itemCount=%d, totalCents=%d, status=%s]",
                id, customerId, itemCount, totalCents, status);
    }
}
