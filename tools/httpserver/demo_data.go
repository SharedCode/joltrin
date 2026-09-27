package main

import (
	"cmp"
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/database"
)

// Demo Data Structures

type UserProfile struct {
	// ID removed to avoid split brain with Key
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Email     string `json:"email"`
	Country   string `json:"country"`
	Age       int    `json:"age"`
	Gender    string `json:"gender"`
}

type Product struct {
	// ID removed
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Category    string  `json:"category"`
	Price       float64 `json:"price"`
	Stock       int     `json:"stock"`
}

type Order struct {
	// ID removed
	// UserID removed for normalization. Relationship managed via users_orders store.
	OrderDate   time.Time   `json:"order_date"`
	TotalAmount float64     `json:"total_amount"`
	Status      string      `json:"status"`
	Items       []OrderItem `json:"items"`
}

type OrderItem struct {
	ProductID string  `json:"product_id"`
	Quantity  int     `json:"quantity"`
	Price     float64 `json:"price"`
}

// PopulateDemoData inserts sample data into the database.
func PopulateDemoData(ctx context.Context, opts sop.DatabaseOptions) error {
	trans, err := database.BeginTransaction(ctx, opts, sop.ForWriting)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %v", err)
	}

	intComparer := func(a, b int) int {
		return cmp.Compare(a, b)
	}

	// 1. Create Stores
	userStoreOpts := sop.StoreOptions{
		Name:                     "users",
		IsValueDataInNodeSegment: true,
		Relations: []sop.Relation{
			{
				SourceFields: []string{"age"},
				TargetStore:  "users_by_age",
				TargetFields: []string{"key"},
			},
			{
				SourceFields: []string{"key"},
				TargetStore:  "users_orders",
				TargetFields: []string{"key"},
			},
		},
	}
	userStore, err := database.NewBtree[uuid.UUID, UserProfile](ctx, opts, "users", trans, nil, userStoreOpts)
	if err != nil {
		if strings.Contains(err.Error(), "exists") {
			userStore, err = database.OpenBtree[uuid.UUID, UserProfile](ctx, opts, "users", trans, nil)
			if err != nil {
				trans.Rollback(ctx)
				return fmt.Errorf("failed to open existing users store: %v", err)
			}
		} else {
			trans.Rollback(ctx)
			return fmt.Errorf("failed to create users store: %v", err)
		}
	}

	// Create users_by_age store for secondary index
	usersByAgeOpts := sop.StoreOptions{
		Name:                     "users_by_age",
		IsUnique:                 false, // Allow duplicate ages
		IsValueDataInNodeSegment: true,
		Description:              "Index of users by age. Key=Age. Value=User ID.",
		Relations: []sop.Relation{
			{
				SourceFields: []string{"value"},
				TargetStore:  "users",
				TargetFields: []string{"key"},
			},
		},
	}
	userByAgeStore, err := database.NewBtree[int, uuid.UUID](ctx, opts, "users_by_age", trans, intComparer, usersByAgeOpts)
	if err != nil {
		if strings.Contains(err.Error(), "exists") {
			userByAgeStore, err = database.OpenBtree[int, uuid.UUID](ctx, opts, "users_by_age", trans, intComparer)
			if err != nil {
				trans.Rollback(ctx)
				return fmt.Errorf("failed to open existing users_by_age store: %v", err)
			}
		} else {
			trans.Rollback(ctx)
			return fmt.Errorf("failed to create users_by_age store: %v", err)
		}
	}

	productStoreOpts := sop.StoreOptions{
		Name:                     "products",
		IsValueDataInNodeSegment: true,
		// Default is 2000 if not specified.
		//SlotLength: 2000,
	}
	productStore, err := database.NewBtree[uuid.UUID, Product](ctx, opts, "products", trans, nil, productStoreOpts)
	if err != nil {
		if strings.Contains(err.Error(), "exists") {
			productStore, err = database.OpenBtree[uuid.UUID, Product](ctx, opts, "products", trans, nil)
			if err != nil {
				trans.Rollback(ctx)
				return fmt.Errorf("failed to open existing products store: %v", err)
			}
		} else {
			trans.Rollback(ctx)
			return fmt.Errorf("failed to create products store: %v", err)
		}
	}

	orderStoreOpts := sop.StoreOptions{
		Name: "orders",
		Relations: []sop.Relation{
			{
				SourceFields: []string{"key"},
				TargetStore:  "users_orders",
				TargetFields: []string{"value"},
			},
		},
	}
	orderStore, err := database.NewBtree[uuid.UUID, Order](ctx, opts, "orders", trans, nil, orderStoreOpts)
	if err != nil {
		if strings.Contains(err.Error(), "exists") {
			orderStore, err = database.OpenBtree[uuid.UUID, Order](ctx, opts, "orders", trans, nil)
			if err != nil {
				trans.Rollback(ctx)
				return fmt.Errorf("failed to open existing orders store: %v", err)
			}
		} else {
			trans.Rollback(ctx)
			return fmt.Errorf("failed to create orders store: %v", err)
		}
	}

	// Create users_orders link table
	usersOrdersOpts := sop.StoreOptions{
		Name:                     "users_orders",
		IsUnique:                 false, // 1 user -> many orders
		IsValueDataInNodeSegment: true,  // Optimize for speed
		Description:              "Link table: UserID -> OrderID",
		Relations: []sop.Relation{
			{
				SourceFields: []string{"key"},
				TargetStore:  "users",
				TargetFields: []string{"key"},
			},
			{
				SourceFields: []string{"value"},
				TargetStore:  "orders",
				TargetFields: []string{"key"},
			},
		},
	}
	// Key = UserID, Value = OrderID
	usersOrdersStore, err := database.NewBtree[uuid.UUID, uuid.UUID](ctx, opts, "users_orders", trans, nil, usersOrdersOpts)
	if err != nil {
		if strings.Contains(err.Error(), "exists") {
			usersOrdersStore, err = database.OpenBtree[uuid.UUID, uuid.UUID](ctx, opts, "users_orders", trans, nil)
			if err != nil {
				trans.Rollback(ctx)
				return fmt.Errorf("failed to open existing users_orders store: %v", err)
			}
		} else {
			trans.Rollback(ctx)
			return fmt.Errorf("failed to create users_orders store: %v", err)
		}
	}

	// 2. Generate Users
	users := make([]struct {
		ID      uuid.UUID
		Profile UserProfile
	}, 0, 50)
	firstNames := []string{"James", "Mary", "John", "Patricia", "Robert", "Jennifer", "Michael", "Linda", "William", "Elizabeth"}
	lastNames := []string{"Smith", "Johnson", "Williams", "Brown", "Jones", "Garcia", "Miller", "Davis", "Rodriguez", "Martinez"}
	countries := []string{"USA", "Canada", "UK", "Germany", "France", "Australia", "Japan"}

	for i := 0; i < 50; i++ {
		fn := firstNames[rand.Intn(len(firstNames))]
		ln := lastNames[rand.Intn(len(lastNames))]
		userID := uuid.New()

		user := UserProfile{
			FirstName: fn,
			LastName:  ln,
			Email:     fmt.Sprintf("%s.%s@example.com", fn, ln),
			Country:   countries[rand.Intn(len(countries))],
			Age:       18 + rand.Intn(60),
			Gender:    []string{"Male", "Female"}[rand.Intn(2)],
		}
		users = append(users, struct {
			ID      uuid.UUID
			Profile UserProfile
		}{userID, user})
		if ok, err := userStore.Add(ctx, userID, user); err != nil || !ok {
			trans.Rollback(ctx)
			return fmt.Errorf("failed to add user: %v", err)
		}
		// Add to secondary index
		if ok, err := userByAgeStore.Add(ctx, user.Age, userID); err != nil || !ok {
			trans.Rollback(ctx)
			return fmt.Errorf("failed to add user to age index: %v", err)
		}
	}

	// 3. Generate Products
	products := make([]struct {
		ID   uuid.UUID
		Item Product
	}, 0, 20)

	productNames := map[string][]string{
		"Electronics": {"Smartphone", "Laptop", "Headphones", "Smart Watch", "Tablet"},
		"Books":       {"Novel", "Science Fiction", "Biography", "Cookbook", "History"},
		"Clothing":    {"T-Shirt", "Jeans", "Jacket", "Sneakers", "Hat"},
		"Home":        {"Coffee Maker", "Blender", "Toaster", "Lamp", "Rug"},
		"Toys":        {"Lego Set", "Action Figure", "Doll", "Puzzle", "Board Game"},
	}

	for cat, names := range productNames {
		for _, name := range names {
			prodID := uuid.New()
			prod := Product{
				Name:        name,
				Description: fmt.Sprintf("High quality %s", name),
				Category:    cat,
				Price:       float64(10 + rand.Intn(990)),
				Stock:       rand.Intn(100),
			}
			products = append(products, struct {
				ID   uuid.UUID
				Item Product
			}{prodID, prod})
			if ok, err := productStore.Add(ctx, prodID, prod); err != nil || !ok {
				trans.Rollback(ctx)
				return fmt.Errorf("failed to add product: %v", err)
			}
		}
	}

	// 4. Generate Orders
	for i := 0; i < 100; i++ {
		user := users[rand.Intn(len(users))]
		numItems := 1 + rand.Intn(5)
		items := make([]OrderItem, 0, numItems)
		total := 0.0

		for j := 0; j < numItems; j++ {
			prod := products[rand.Intn(len(products))]
			qty := 1 + rand.Intn(3)
			item := OrderItem{
				ProductID: prod.ID.String(),
				Quantity:  qty,
				Price:     prod.Item.Price,
			}
			items = append(items, item)
			total += float64(qty) * prod.Item.Price
		}

		orderID := uuid.New()

		order := Order{
			// UserID removed
			OrderDate:   time.Now().Add(-time.Duration(rand.Intn(30*24)) * time.Hour), // Past 30 days
			TotalAmount: total,
			Status:      []string{"Pending", "Shipped", "Delivered", "Cancelled"}[rand.Intn(4)],
			Items:       items,
		}

		if ok, err := orderStore.Add(ctx, orderID, order); err != nil || !ok {
			trans.Rollback(ctx)
			return fmt.Errorf("failed to add order: %v", err)
		}

		// Add link to users_orders
		if ok, err := usersOrdersStore.Add(ctx, user.ID, orderID); err != nil || !ok {
			trans.Rollback(ctx)
			return fmt.Errorf("failed to add users_orders link: %v", err)
		}
	}

	return trans.Commit(ctx)
}
