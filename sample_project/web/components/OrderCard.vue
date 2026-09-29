<template>
  <div class="order-card">
    <h3>Заказ №{{ order.id }}</h3>
    <p>Итого: {{ formattedTotal }}</p>
    <button @click="pay">Оплатить</button>
  </div>
</template>

<script>
// OrderCard — карточка заказа с кнопкой оплаты.
// Displays total with discount applied on the server.
import { createOrder } from "../api/client.js";

export default {
  name: "OrderCard",
  props: {
    order: { type: Object, required: true },
    token: { type: String, required: true },
  },
  computed: {
    // formattedTotal форматирует сумму в рублях.
    formattedTotal() {
      return `${this.order.total.toFixed(2)} ₽`;
    },
  },
  methods: {
    // pay инициирует оплату: создаёт invoice через billing service.
    async pay() {
      const invoice = await createOrder(this.token, this.order.items);
      this.$emit("paid", invoice);
    },
  },
};
</script>

<style scoped>
.order-card {
  border: 1px solid #ccc;
  padding: 1rem;
}
</style>
