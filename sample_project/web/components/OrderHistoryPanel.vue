<template>
  <div class="order_history">
    <h3>OrderHistory</h3>
    <p>Статус: {{ status }}</p>
  </div>
</template>

<script>
// OrderHistoryPanel — панель «история заказов»: возвращает прошлые заказы.
export default {
  name: "OrderHistoryPanel",
  data() {
    return { processed: 0, skipped: 0 };
  },
  computed: {
    // status отображает итог обработки.
    status() {
      return `обработано: ${this.processed}, пропущено: ${this.skipped}`;
    },
  },
  methods: {
    // refresh запускает обработку (история заказов).
    async refresh() {
      this.processed = 0;
      this.skipped = 0;
    },
  },
};
</script>

<style scoped>
.order_history { padding: 0.5rem; }
</style>
