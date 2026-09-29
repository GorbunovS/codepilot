// Package orders реализует оформление заказов и скидки.
package orders

import "errors"

// ErrEmptyOrder — заказ без позиций не допускается.
var ErrEmptyOrder = errors.New("order has no items")

// Item — позиция заказа.
type Item struct {
	SKU      string
	Quantity int
	Price    float64 // цена за единицу, в рублях
}

// Order — заказ пользователя.
type Order struct {
	ID     string
	UserID string
	Items  []Item
	Total  float64
}

// CalculateDiscount вычисляет скидку по сумме заказа.
// Business rule: >= 10000 RUB -> 10%, >= 5000 -> 5%.
func CalculateDiscount(subtotal float64) float64 {
	switch {
	case subtotal >= 10000:
		return subtotal * 0.10
	case subtotal >= 5000:
		return subtotal * 0.05
	default:
		return 0
	}
}

// CreateOrder собирает заказ и считает итоговую сумму со скидкой.
func CreateOrder(id, userID string, items []Item) (Order, error) {
	if len(items) == 0 {
		return Order{}, ErrEmptyOrder
	}
	var subtotal float64
	for _, it := range items {
		subtotal += it.Price * float64(it.Quantity)
	}
	total := subtotal - CalculateDiscount(subtotal)
	return Order{ID: id, UserID: userID, Items: items, Total: total}, nil
}
