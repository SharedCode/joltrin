# Interoperability Demo: Secondary Indexes

This example demonstrates how to use SOP's `jsondb` package to create a "schema-less" store with composite secondary indexes. This approach ensures that your data sorting and indexing rules are compatible across all SOP language bindings (Go, Python, Java, C#, Rust).

## Overview

The example creates a database of products and indexes them by:
1.  **Category** (Ascending)
2.  **Price** (Descending)

This means products will be grouped by category, and within each category, the most expensive items will appear first.

## Key Features

*   **`jsondb.NewJsonBtreeMapKey`**: Uses a dynamic map for keys, allowing flexible fields.
*   **`IndexSpecification`**: Defines the sorting order for multiple fields.
*   **Data Generation**: The script automatically generates **500 records** across various categories (Electronics, Books, Clothing, Home, Garden) to populate the database for testing and visualization.

## Running the Example (Map-based)

This version uses `map[string]any` for keys, which is useful for dynamic schemas.

```bash
go run ./interop_indexes
```
*Data is stored in: `./data/interop_indexes`*

## Recommended: Idiomatic Go Structs (Data Generator)

We recommend using the struct-based approach for most Go applications. This example also serves as the **primary data generator** for testing the Data Management Suite, creating 500 records.

This allows you to define your key as a struct:
```go
type ProductKey struct {
    Category string  `json:"category"`
    Price    float64 `json:"price"`
    ID       string  `json:"id"`
}
```

Run the struct-based example to populate the database:
```bash
go run ./interop_indexes/struct_key_example
```

## Verifying with Data Management Suite

After running the struct-based example, you can use the **SOP Data Management Suite** to inspect the data.

1.  Start the Data Management Suite:
    ```bash
    go run tools/httpserver/main.go -registry ./data/struct_key_demo
    ```
2.  Open [http://localhost:8080](http://localhost:8080).
3.  Select the `products_struct` store.
4.  You can now browse the 500 generated records, navigate through pages, and search for specific items using the complex key search feature.
