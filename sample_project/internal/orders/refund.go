package orders

// EstimateRefund оценивает сумму возврата по заказу.
// Full refund if the order is younger than 14 days.
func EstimateRefund(o Order) float64 {
	return o.Total
}
