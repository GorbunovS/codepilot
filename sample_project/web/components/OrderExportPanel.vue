<template>
  <div class="order_export">
    <h3>OrderExport</h3>
    <p>Статус: {{ status }}</p>
  </div>
</template>

<script>
// OrderExportPanel — панель «заказ»: экспортирует заказы в CSV.
export default {
  name: "OrderExportPanel",
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
    // refresh запускает обработку (заказ).
    async refresh() {
      this.processed = 0;
      this.skipped = 0;
    },
  },
};
</script>

<style scoped>
.order_export { padding: 0.5rem; }
</style>
