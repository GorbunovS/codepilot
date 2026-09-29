// API client for the orders backend.
// Обёртка над fetch с обработкой ошибок и токеном авторизации.

const API_BASE = "/api/v1";

/**
 * handleError преобразует HTTP-ошибку в понятное исключение.
 * @param {Response} resp
 */
async function handleError(resp) {
  if (!resp.ok) {
    const body = await resp.text();
    throw new Error(`API error ${resp.status}: ${body}`);
  }
  return resp.json();
}

/**
 * fetchOrders загружает список заказов пользователя.
 * @param {string} token — токен сессии из auth service
 */
export async function fetchOrders(token) {
  const resp = await fetch(`${API_BASE}/orders`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  return handleError(resp);
}

/**
 * createOrder отправляет заказ на бэкенд.
 * Скидка считается на сервере (см. CalculateDiscount в Go).
 */
export async function createOrder(token, items) {
  const resp = await fetch(`${API_BASE}/orders`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ items }),
  });
  return handleError(resp);
}
