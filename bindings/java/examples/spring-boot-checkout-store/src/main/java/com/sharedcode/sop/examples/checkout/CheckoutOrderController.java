package com.sharedcode.sop.examples.checkout;

import com.sharedcode.sop.SopException;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.*;

import java.util.List;
import java.util.Map;
import java.util.UUID;

@RestController
@RequestMapping("/orders")
public class CheckoutOrderController {

    private final CheckoutOrderStore store;

    public CheckoutOrderController(CheckoutOrderStore store) {
        this.store = store;
    }

    @PostMapping
    public ResponseEntity<CheckoutOrder> create(@RequestBody Map<String, Object> body) throws SopException {
        String id = UUID.randomUUID().toString();
        CheckoutOrder order = new CheckoutOrder(
                id,
                (String) body.get("customerId"),
                ((Number) body.getOrDefault("itemCount", 0)).intValue(),
                ((Number) body.getOrDefault("totalCents", 0)).longValue(),
                "PENDING");
        return ResponseEntity.ok(store.create(order));
    }

    @GetMapping("/{id}")
    public ResponseEntity<CheckoutOrder> get(@PathVariable String id) throws SopException {
        CheckoutOrder order = store.get(id);
        return order == null ? ResponseEntity.notFound().build() : ResponseEntity.ok(order);
    }

    @PutMapping("/{id}/status")
    public ResponseEntity<CheckoutOrder> updateStatus(@PathVariable String id, @RequestBody Map<String, String> body) throws SopException {
        CheckoutOrder updated = store.updateStatus(id, body.get("status"));
        return updated == null ? ResponseEntity.notFound().build() : ResponseEntity.ok(updated);
    }

    @DeleteMapping("/{id}")
    public ResponseEntity<Void> delete(@PathVariable String id) throws SopException {
        boolean removed = store.delete(id);
        return removed ? ResponseEntity.noContent().build() : ResponseEntity.notFound().build();
    }

    @GetMapping
    public ResponseEntity<List<CheckoutOrder>> list() throws SopException {
        return ResponseEntity.ok(store.list());
    }
}
