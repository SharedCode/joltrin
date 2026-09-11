package com.sharedcode.sop.examples.checkout;

import com.sharedcode.sop.BTree;
import com.sharedcode.sop.Context;
import com.sharedcode.sop.Database;
import com.sharedcode.sop.DatabaseOptions;
import com.sharedcode.sop.DatabaseType;
import com.sharedcode.sop.Item;
import com.sharedcode.sop.SopException;
import com.sharedcode.sop.Transaction;
import com.sharedcode.sop.TransactionMode;
import jakarta.annotation.PostConstruct;
import org.springframework.stereotype.Service;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

/**
 * Wraps the real sop4j B-Tree as this service's persistence layer, no JPA, no
 * separate database process, the checkout order store IS the embedded SOP
 * B-Tree on disk. One Database instance for the whole app, a fresh
 * Context/Transaction per operation, same lifecycle the binding's own
 * TUTORIAL.md demonstrates.
 */
@Service
public class CheckoutOrderStore {

    private static final String STORE_NAME = "checkout_orders";

    private final Database database;

    public CheckoutOrderStore() {
        DatabaseOptions options = new DatabaseOptions();
        options.stores_folders = Collections.singletonList("checkout_data");
        options.type = DatabaseType.Standalone;
        this.database = new Database(options);
    }

    @PostConstruct
    public void createStoreIfMissing() throws SopException {
        try (Context ctx = new Context()) {
            try (Transaction tx = database.beginTransaction(ctx)) {
                BTree.create(ctx, STORE_NAME, tx, null, String.class, CheckoutOrder.class);
                tx.commit();
            }
        } catch (SopException e) {
            // A second app instance racing to create the same store on first boot
            // is the one case worth tolerating quietly; anything else surfaces.
            if (e.getMessage() == null || !e.getMessage().toLowerCase().contains("exist")) {
                throw e;
            }
        }
    }

    public CheckoutOrder create(CheckoutOrder order) throws SopException {
        try (Context ctx = new Context()) {
            try (Transaction tx = database.beginTransaction(ctx)) {
                BTree<String, CheckoutOrder> orders =
                        BTree.open(ctx, STORE_NAME, tx, String.class, CheckoutOrder.class);
                orders.add(order.id, order);
                tx.commit();
            }
        }
        return order;
    }

    public CheckoutOrder get(String id) throws SopException {
        try (Context ctx = new Context()) {
            try (Transaction tx = database.beginTransaction(ctx, TransactionMode.ForReading)) {
                BTree<String, CheckoutOrder> orders =
                        BTree.open(ctx, STORE_NAME, tx, String.class, CheckoutOrder.class);
                if (!orders.find(id)) {
                    return null;
                }
                Item<String, CheckoutOrder> item = orders.getCurrentValue();
                return item.value;
            }
        }
    }

    public CheckoutOrder updateStatus(String id, String newStatus) throws SopException {
        try (Context ctx = new Context()) {
            try (Transaction tx = database.beginTransaction(ctx)) {
                BTree<String, CheckoutOrder> orders =
                        BTree.open(ctx, STORE_NAME, tx, String.class, CheckoutOrder.class);
                if (!orders.find(id)) {
                    tx.rollback();
                    return null;
                }
                CheckoutOrder order = orders.getCurrentValue().value;
                order.status = newStatus;
                orders.update(id, order);
                tx.commit();
                return order;
            }
        }
    }

    public boolean delete(String id) throws SopException {
        try (Context ctx = new Context()) {
            try (Transaction tx = database.beginTransaction(ctx)) {
                BTree<String, CheckoutOrder> orders =
                        BTree.open(ctx, STORE_NAME, tx, String.class, CheckoutOrder.class);
                boolean removed = orders.remove(id);
                tx.commit();
                return removed;
            }
        }
    }

    public List<CheckoutOrder> list() throws SopException {
        List<CheckoutOrder> result = new ArrayList<>();
        try (Context ctx = new Context()) {
            try (Transaction tx = database.beginTransaction(ctx, TransactionMode.ForReading)) {
                BTree<String, CheckoutOrder> orders =
                        BTree.open(ctx, STORE_NAME, tx, String.class, CheckoutOrder.class);
                if (orders.first()) {
                    do {
                        result.add(orders.getCurrentValue().value);
                    } while (orders.next());
                }
            }
        }
        return result;
    }
}
