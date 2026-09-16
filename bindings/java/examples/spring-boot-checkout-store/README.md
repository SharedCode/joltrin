# spring-boot-checkout-store

A small Spring Boot REST service using the real `sop4j` binding as its
persistence layer instead of JPA or a separate database process. The
checkout order store is a `sop4j` B-Tree on disk, `CheckoutOrderStore`
opens a real `Context`/`Transaction` per request and calls `add`,
`find`, `update`, `remove`, `first`/`next` against it, same API the
binding's own `TUTORIAL.md` teaches.

## Why this exists

The Go core and the Python, Rust, and C# bindings already had real
demos in this repo. Java didn't, the Java binding existed at the
JNA/native-loading level (`bindings/java/src`, `pom.xml`) but nothing
showed it wired into an actual application someone would recognize,
a Spring Boot service. This closes that gap with real code, not a
comparison writeup.

## What's verified, and what isn't

- **Compiles clean** against the real `sop4j-5.5.0` jar (`mvn install`
  in `bindings/java` first, then `mvn compile` here), no mocked or
  guessed API calls. Doing this caught a real bug: `TUTORIAL.md`'s
  Step 5 calls `products.updateCurrentValue(item)`, that method doesn't
  exist on `BTree` anymore, the current API is `update(key, value)` or
  `update(item)`. This example uses the real, current method.
- **Not runtime-verified here.** Running the app requires the native
  `libjsondb` shared library that `sop4j`'s JNA layer loads at startup,
  built by this repo's own Docker cross-compilation pipeline, not
  something a plain `mvn spring-boot:run` produces on its own. Anyone
  running this against a build that includes that native library gets
  a working REST API; anyone building just this Maven module in
  isolation will hit `UnsatisfiedLinkError` at the `@PostConstruct`
  step, and that's expected, not a bug in this example.

## Run it (once the native library is available on the classpath/library path)

```bash
cd ../..            # bindings/java
mvn install          # builds and installs sop4j locally

cd examples/spring-boot-checkout-store
mvn spring-boot:run
```

## API

```bash
# create
curl -X POST localhost:8080/orders \
  -H 'Content-Type: application/json' \
  -d '{"customerId":"cust_1","itemCount":3,"totalCents":4599}'

# read
curl localhost:8080/orders/<id>

# advance status
curl -X PUT localhost:8080/orders/<id>/status \
  -H 'Content-Type: application/json' \
  -d '{"status":"PAID"}'

# list
curl localhost:8080/orders

# delete
curl -X DELETE localhost:8080/orders/<id>
```
